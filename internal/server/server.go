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

	"github.com/flashdict/kindle2flashdict/internal/task"
)

type Config struct {
	WebAddr    string
	KindleAddr string
}

type Server struct {
	config  Config
	handler Handler
	runner  *task.Runner
	stdout  io.Writer
}

func NewServer(config Config, handler Handler, runner *task.Runner, stdout io.Writer) Server {
	if runner == nil {
		panic("server.NewServer requires a task runner")
	}
	return Server{config: config, handler: handler, runner: runner, stdout: stdout}
}

func (s Server) Run() error {
	return s.RunContext(context.Background())
}

func (s Server) RunContext(ctx context.Context) error {
	runCtx, cancelRun := context.WithCancel(ctx)
	defer cancelRun()
	if err := s.runner.Prepare(runCtx); err != nil {
		return err
	}
	webAddr := s.config.WebAddr
	if webAddr == "" {
		webAddr = ":8787"
	}
	kindleAddr := s.config.KindleAddr
	if kindleAddr == "" {
		kindleAddr = ":8788"
	}
	if s.stdout != nil {
		printURLs(s.stdout, webAddr, kindleAddr)
	}

	webServer := &http.Server{Addr: webAddr, Handler: s.handler.WebMux()}
	kindleServer := &http.Server{Addr: kindleAddr, Handler: s.handler.KindleMux()}
	errCh := make(chan error, 3)
	go func() {
		errCh <- webServer.ListenAndServe()
	}()
	go func() {
		errCh <- kindleServer.ListenAndServe()
	}()
	runnerDone := make(chan error, 1)
	go func() {
		err := s.runner.Run(runCtx)
		runnerDone <- err
		errCh <- err
	}()
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
