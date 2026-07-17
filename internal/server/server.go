package server

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"time"
)

type Config struct {
	WebAddr    string
	KindleAddr string
	LibraryDir string
}

type Server struct {
	Config  Config
	Handler Handler
	Runner  interface{ Run(context.Context) error }
	Stdout  io.Writer
}

func (s Server) Run() error {
	return s.RunContext(context.Background())
}

func (s Server) RunContext(ctx context.Context) error {
	runCtx, cancelRun := context.WithCancel(ctx)
	defer cancelRun()
	if preparer, ok := s.Runner.(interface{ Prepare(context.Context) error }); ok {
		if err := preparer.Prepare(runCtx); err != nil {
			return err
		}
	}
	webAddr := s.Config.WebAddr
	if webAddr == "" {
		webAddr = ":8787"
	}
	kindleAddr := s.Config.KindleAddr
	if kindleAddr == "" {
		kindleAddr = ":8788"
	}
	if s.Stdout != nil {
		printURLs(s.Stdout, webAddr, kindleAddr)
	}

	webServer := &http.Server{Addr: webAddr, Handler: s.Handler.WebMux()}
	kindleServer := &http.Server{Addr: kindleAddr, Handler: s.Handler.KindleMux()}
	errCh := make(chan error, 3)
	go func() {
		errCh <- webServer.ListenAndServe()
	}()
	go func() {
		errCh <- kindleServer.ListenAndServe()
	}()
	runnerDone := make(chan error, 1)
	if s.Runner != nil {
		go func() {
			err := s.Runner.Run(runCtx)
			runnerDone <- err
			errCh <- err
		}()
	}
	var runErr error
	select {
	case <-ctx.Done():
		runErr = ctx.Err()
	case runErr = <-errCh:
	}
	cancelRun()
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = webServer.Shutdown(shutdownCtx)
	_ = kindleServer.Shutdown(shutdownCtx)
	if s.Runner != nil {
		select {
		case runnerErr := <-runnerDone:
			if runErr == nil && runnerErr != nil {
				runErr = runnerErr
			}
		case <-shutdownCtx.Done():
			if runErr == nil {
				runErr = fmt.Errorf("task runner did not stop before shutdown timeout")
			}
		}
	}
	if errors.Is(runErr, http.ErrServerClosed) {
		return nil
	}
	return runErr
}

func printURLs(w io.Writer, webAddr, kindleAddr string) {
	fmt.Fprintln(w, "Kindle Go server started.")
	fmt.Fprintln(w)
	fmt.Fprintln(w, "Web UI:")
	for _, url := range URLs(webAddr) {
		fmt.Fprintf(w, "  %s\n", url)
	}
	fmt.Fprintln(w)
	fmt.Fprintln(w, "Kindle:")
	for _, url := range URLs(kindleAddr) {
		fmt.Fprintf(w, "  %s\n", url)
	}
}

func URLs(addr string) []string {
	host, port, ok := splitAddr(addr)
	if !ok {
		return []string{"http://" + addr + "/"}
	}
	if host == "" || host == "0.0.0.0" || host == "::" {
		var urls []string
		urls = append(urls, "http://127.0.0.1:"+port+"/")
		for _, ip := range localIPv4s() {
			urls = append(urls, "http://"+ip+":"+port+"/")
		}
		return urls
	}
	if strings.Contains(host, ":") {
		host = "[" + strings.Trim(host, "[]") + "]"
	}
	return []string{"http://" + host + ":" + port + "/"}
}

func splitAddr(addr string) (host, port string, ok bool) {
	if strings.HasPrefix(addr, ":") {
		return "", strings.TrimPrefix(addr, ":"), true
	}
	host, port, err := net.SplitHostPort(addr)
	return host, port, err == nil
}

func localIPv4s() []string {
	ifaces, err := net.Interfaces()
	if err != nil {
		return nil
	}
	var out []string
	for _, iface := range ifaces {
		if iface.Flags&net.FlagUp == 0 || iface.Flags&net.FlagLoopback != 0 {
			continue
		}
		addrs, err := iface.Addrs()
		if err != nil {
			continue
		}
		for _, addr := range addrs {
			var ip net.IP
			switch v := addr.(type) {
			case *net.IPNet:
				ip = v.IP
			case *net.IPAddr:
				ip = v.IP
			}
			if ip4 := ip.To4(); ip4 != nil {
				out = append(out, ip4.String())
			}
		}
	}
	return out
}
