package server

import (
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
)

type Config struct {
	WebAddr    string
	KindleAddr string
	LibraryDir string
}

type Server struct {
	Config  Config
	Handler Handler
	Stdout  io.Writer
}

func (s Server) Run() error {
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

	errCh := make(chan error, 2)
	go func() {
		errCh <- http.ListenAndServe(webAddr, s.Handler.WebMux())
	}()
	go func() {
		errCh <- http.ListenAndServe(kindleAddr, s.Handler.KindleMux())
	}()
	return <-errCh
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
