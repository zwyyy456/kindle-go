package main

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"io"
	"math/big"
	"net"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"
)

const (
	defaultAddr       = ":8790"
	defaultSessionTTL = 10 * time.Minute
	maxSignalBody     = 64 << 10
	maxReportBody     = 32 << 10
	maxControlBody    = 256 << 10
	maxSignalMessages = 16
	maxSessionSignals = 512 << 10
)

type Server struct {
	Addr            string
	Stdout          io.Writer
	store           signalStore
	control         *sessionStore
	publicURL       string
	storageBaseURL  string
	storageSecret   []byte
	offlineLifetime time.Duration
	r2              objectBackend
	stunURLs        []string
	httpClient      *http.Client
}

func (s *Server) Run() error {
	addr := s.Addr
	if addr == "" {
		addr = defaultAddr
	}
	if s.Stdout != nil {
		fmt.Fprintln(s.Stdout, "WebRTC demo server started.")
		fmt.Fprintln(s.Stdout)
		for _, url := range urls(addr) {
			fmt.Fprintf(s.Stdout, "  %s\n", url)
		}
	}
	server := &http.Server{
		Addr:              addr,
		Handler:           s.routes(),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       15 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       60 * time.Second,
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	server.RegisterOnShutdown(cancel)
	if s.control != nil {
		go s.runCleanup(ctx)
	}
	return server.ListenAndServe()
}

func (s *Server) routes() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/", s.handleIndex)
	mux.HandleFunc("/sender", s.handleSender)
	mux.HandleFunc("/receiver", s.handleReceiver)
	mux.HandleFunc("/diagnostics", s.handleDiagnostics)
	mux.HandleFunc("/api/session", s.handleCreateSession)
	mux.HandleFunc("/api/transfers", s.handleTransfers)
	mux.HandleFunc("/api/transfers/", s.handleTransferAction)
	mux.HandleFunc("/api/receive/", s.handleReceive)
	mux.HandleFunc("/api/host", s.handleHost)
	mux.HandleFunc("/api/config", s.handleClientConfig)
	mux.HandleFunc("/api/capabilities", s.handleCapabilities)
	mux.HandleFunc("/api/signal", s.handleSignal)
	mux.HandleFunc("/api/poll", s.handlePoll)
	mux.HandleFunc("/ws-probe", s.handleWebSocketProbe)
	mux.HandleFunc("/sw.js", s.handleServiceWorker)
	mux.HandleFunc("/sha256-worker.js", s.handleSHA256Worker)
	mux.HandleFunc("/http-sample.txt", s.handleHTTPSample)
	return mux
}

func (s *Server) handleClientConfig(w http.ResponseWriter, r *http.Request) {
	if !allowMethod(w, r, http.MethodGet) {
		return
	}
	writeJSON(w, map[string]any{
		"stun_urls":        s.stunURLs,
		"max_file_size":    maxFileSize,
		"r2_enabled":       s.r2 != nil,
		"server_a_enabled": s.storageBaseURL != "" && len(s.storageSecret) > 0,
	})
}

func (s *Server) handleSHA256Worker(w http.ResponseWriter, r *http.Request) {
	if !allowMethod(w, r, http.MethodGet) {
		return
	}
	w.Header().Set("Content-Type", "application/javascript; charset=utf-8")
	w.Header().Set("Cache-Control", "public, max-age=3600")
	io.WriteString(w, sha256WorkerJS)
}

func (s *Server) handleHost(w http.ResponseWriter, r *http.Request) {
	if !allowMethod(w, r, http.MethodGet) {
		return
	}
	host := hostWithoutPort(r.Host)
	if host == "" || host == "localhost" || strings.HasPrefix(host, "127.") || host == "::1" || host == "[::1]" {
		host = preferredLocalIPv4()
	}
	writeJSON(w, map[string]string{"host": host})
}

func (s *Server) handleIndex(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}
	if !allowMethod(w, r, http.MethodGet) {
		return
	}
	writeHTML(w, receiverHTML)
}

func (s *Server) handleSender(w http.ResponseWriter, r *http.Request) {
	if !allowMethod(w, r, http.MethodGet) {
		return
	}
	writeHTML(w, senderHTML)
}

func (s *Server) handleReceiver(w http.ResponseWriter, r *http.Request) {
	if !allowMethod(w, r, http.MethodGet) {
		return
	}
	writeHTML(w, receiverHTML)
}

func (s *Server) handleDiagnostics(w http.ResponseWriter, r *http.Request) {
	if !allowMethod(w, r, http.MethodGet) {
		return
	}
	writeHTML(w, receiverHTML)
}

func (s *Server) handleCreateSession(w http.ResponseWriter, r *http.Request) {
	if !allowMethod(w, r, http.MethodPost) {
		return
	}
	if err := requireSmallOrEmptyBody(w, r, maxControlBody); err != nil {
		writeDecodeError(w, err)
		return
	}
	var id string
	var managementToken string
	if s.control != nil {
		session, token, err := s.control.create(modeOnline, "online-transfer", "application/octet-stream", 0, "")
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		if !s.store.create(session.Code) {
			http.Error(w, "failed to allocate signal session", http.StatusInternalServerError)
			return
		}
		id, managementToken = session.Code, token
	}
	for i := 0; id == "" && i < 20; i++ {
		candidate, err := randomID(6)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		if s.store.create(candidate) {
			id = candidate
			break
		}
	}
	if id == "" {
		http.Error(w, "failed to allocate token", http.StatusInternalServerError)
		return
	}
	writeJSON(w, map[string]string{
		"id": id, "receiver_path": "/receiver?id=" + id,
		"management_token": managementToken,
	})
}

func (s *Server) handleCapabilities(w http.ResponseWriter, r *http.Request) {
	if !allowMethod(w, r, http.MethodPost) {
		return
	}
	var report capabilityReport
	if err := decodeStrictJSON(w, r, maxReportBody, &report); err != nil {
		writeDecodeError(w, err)
		return
	}
	if len(report.UserAgent) > 2048 || len(report.Lines) > 64 {
		http.Error(w, "capability report too large", http.StatusBadRequest)
		return
	}
	for _, line := range report.Lines {
		if len(line) > 512 {
			http.Error(w, "capability report line too large", http.StatusBadRequest)
			return
		}
	}
	if s.Stdout != nil {
		fmt.Fprintln(s.Stdout)
		fmt.Fprintln(s.Stdout, "WebRTC capability report received:")
		fmt.Fprintf(s.Stdout, "  User-Agent: %s\n", printableLine(report.UserAgent))
		fmt.Fprintf(s.Stdout, "  Secure context: %t\n", report.SecureContext)
		for _, line := range report.Lines {
			fmt.Fprintf(s.Stdout, "  %s\n", printableLine(line))
		}
	}
	writeJSON(w, map[string]bool{"ok": true})
}

func (s *Server) handleSignal(w http.ResponseWriter, r *http.Request) {
	if !allowMethod(w, r, http.MethodPost) {
		return
	}
	var msg signalMessage
	if err := decodeStrictJSON(w, r, maxSignalBody, &msg); err != nil {
		writeDecodeError(w, err)
		return
	}
	msg.Session = strings.TrimSpace(msg.Session)
	msg.From = strings.TrimSpace(msg.From)
	msg.Type = strings.TrimSpace(msg.Type)
	if !validSessionID(msg.Session) || !validSignal(msg) {
		http.Error(w, "invalid signal fields", http.StatusBadRequest)
		return
	}
	seq, ok := s.store.append(msg)
	if !ok {
		http.Error(w, "session not found or expired", http.StatusNotFound)
		return
	}
	writeJSON(w, map[string]int{"seq": seq})
}

func (s *Server) handlePoll(w http.ResponseWriter, r *http.Request) {
	if !allowMethod(w, r, http.MethodGet) {
		return
	}
	session := r.URL.Query().Get("session")
	to := r.URL.Query().Get("to")
	after := 0
	fmt.Sscanf(r.URL.Query().Get("after"), "%d", &after)
	if !validSessionID(session) || (to != "sender" && to != "receiver") || after < 0 {
		http.Error(w, "invalid session, recipient, or sequence", http.StatusBadRequest)
		return
	}
	messages, ok := s.store.messagesFor(session, to, after)
	if !ok {
		http.Error(w, "session not found or expired", http.StatusNotFound)
		return
	}
	writeJSON(w, map[string][]signalMessage{"messages": messages})
}

func (s *Server) handleHTTPSample(w http.ResponseWriter, r *http.Request) {
	if !allowMethod(w, r, http.MethodGet) {
		return
	}
	w.Header().Set("Content-Disposition", `attachment; filename="kindle-http-sample.txt"`)
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	fmt.Fprintln(w, "This file was downloaded over plain HTTP, not WebRTC.")
	fmt.Fprintln(w, "If this works on Kindle but the WebRTC receiver does not, the Kindle browser is using the HTTP path.")
}

func (s *Server) handleServiceWorker(w http.ResponseWriter, r *http.Request) {
	if !allowMethod(w, r, http.MethodGet) {
		return
	}
	w.Header().Set("Content-Type", "application/javascript; charset=utf-8")
	w.Header().Set("Service-Worker-Allowed", "/")
	io.WriteString(w, serviceWorkerJS)
}

func writeHTML(w http.ResponseWriter, page string) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	io.WriteString(w, page)
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	_ = json.NewEncoder(w).Encode(v)
}

func writeJSONStatus(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func allowMethod(w http.ResponseWriter, r *http.Request, method string) bool {
	if r.Method == method {
		return true
	}
	w.Header().Set("Allow", method)
	http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	return false
}

type signalStore struct {
	mu       sync.Mutex
	sessions map[string]*signalSession
	now      func() time.Time
}

type signalSession struct {
	nextSeq       int
	expires       time.Time
	uploadStarted bool
	signalBytes   int
	messages      []signalMessage
}

type signalMessage struct {
	Seq     int             `json:"seq"`
	Session string          `json:"session"`
	From    string          `json:"from"`
	Type    string          `json:"type"`
	Data    json.RawMessage `json:"data"`
}

type capabilityReport struct {
	UserAgent     string   `json:"user_agent"`
	SecureContext bool     `json:"secure_context"`
	Lines         []string `json:"lines"`
}

func (s *signalStore) create(id string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.currentTime()
	s.removeExpired(now)
	if s.sessions == nil {
		s.sessions = make(map[string]*signalSession)
	}
	if _, ok := s.sessions[id]; ok {
		return false
	}
	s.sessions[id] = &signalSession{expires: now.Add(defaultSessionTTL)}
	return true
}

func (s *signalStore) append(msg signalMessage) (int, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.currentTime()
	s.removeExpired(now)
	session := s.sessions[msg.Session]
	if session == nil {
		return 0, false
	}
	messageBytes := len(msg.Session) + len(msg.From) + len(msg.Type) + len(msg.Data)
	if len(session.messages) >= maxSignalMessages || session.signalBytes+messageBytes > maxSessionSignals {
		return 0, false
	}
	session.nextSeq++
	session.signalBytes += messageBytes
	msg.Seq = session.nextSeq
	session.messages = append(session.messages, msg)
	return msg.Seq, true
}

func (s *signalStore) messagesFor(sessionID, to string, after int) ([]signalMessage, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.currentTime()
	s.removeExpired(now)
	session := s.sessions[sessionID]
	if session == nil {
		return nil, false
	}
	var out []signalMessage
	for _, msg := range session.messages {
		if msg.Seq > after && msg.From != to {
			out = append(out, msg)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Seq < out[j].Seq })
	return out, true
}

func (s *signalStore) start(id string, lifetime time.Duration) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.currentTime()
	s.removeExpired(now)
	session := s.sessions[id]
	if session == nil {
		return false
	}
	if !session.uploadStarted {
		session.uploadStarted = true
		session.expires = now.Add(lifetime)
	}
	return true
}

func (s *signalStore) currentTime() time.Time {
	if s.now != nil {
		return s.now()
	}
	return time.Now()
}

func (s *signalStore) removeExpired(now time.Time) {
	for id, session := range s.sessions {
		if !now.Before(session.expires) {
			delete(s.sessions, id)
		}
	}
}

func validSessionID(id string) bool {
	if len(id) != 6 {
		return false
	}
	for _, r := range id {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

func validSignal(msg signalMessage) bool {
	if !json.Valid(msg.Data) {
		return false
	}
	switch msg.From {
	case "sender":
		return msg.Type == "offer"
	case "receiver":
		return msg.Type == "answer"
	default:
		return false
	}
}

func printableLine(value string) string {
	value = strings.Map(func(r rune) rune {
		if r == '\n' || r == '\r' || r == '\t' || r < 0x20 || r == 0x7f {
			return ' '
		}
		return r
	}, value)
	return strings.TrimSpace(value)
}

func randomID(n int) (string, error) {
	const alphabet = "0123456789"
	var b strings.Builder
	for i := 0; i < n; i++ {
		x, err := rand.Int(rand.Reader, big.NewInt(int64(len(alphabet))))
		if err != nil {
			return "", err
		}
		b.WriteByte(alphabet[x.Int64()])
	}
	return b.String(), nil
}

func hostWithoutPort(hostport string) string {
	if hostport == "" {
		return ""
	}
	host, _, err := net.SplitHostPort(hostport)
	if err == nil {
		return strings.Trim(host, "[]")
	}
	return strings.Trim(hostport, "[]")
}

func preferredLocalIPv4() string {
	ifaces, err := net.Interfaces()
	if err != nil {
		return ""
	}
	var fallback string
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
			ip4 := ip.To4()
			if ip4 == nil {
				continue
			}
			text := ip4.String()
			if fallback == "" {
				fallback = text
			}
			if !strings.HasPrefix(text, "169.254.") {
				return text
			}
		}
	}
	return fallback
}

func urls(addr string) []string {
	host, port, ok := splitAddr(addr)
	if !ok {
		return []string{"http://" + addr + "/"}
	}
	if host == "" || host == "0.0.0.0" || host == "::" {
		out := []string{"http://127.0.0.1:" + port + "/"}
		if ip := preferredLocalIPv4(); ip != "" {
			out = append(out, "http://"+ip+":"+port+"/")
		}
		return out
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

const commonJS = `
function $(id) { return document.getElementById(id); }
function log(text) {
  var el = $('log');
  el.value += text + "\n";
  el.scrollTop = el.scrollHeight;
}
function hasWebRTC() {
  return !!(window.RTCPeerConnection || window.webkitRTCPeerConnection || window.mozRTCPeerConnection);
}
function capabilityReport() {
  var lines = [];
  var constructors = [
    "RTCPeerConnection",
    "webkitRTCPeerConnection",
    "mozRTCPeerConnection",
    "webkitPeerConnection00",
    "webkitPeerConnection",
    "PeerConnection",
    "RTCDataChannel",
    "DataChannel",
    "RTCSessionDescription",
    "webkitRTCSessionDescription",
    "mozRTCSessionDescription",
    "SessionDescription",
    "RTCIceCandidate",
    "webkitRTCIceCandidate",
    "mozRTCIceCandidate",
    "IceCandidate"
  ];
  var methods = [
    "createDataChannel",
    "createOffer",
    "createAnswer",
    "setLocalDescription",
    "setRemoteDescription",
    "addIceCandidate",
    "processIceMessage",
    "startIce",
    "addStream"
  ];
  for (var i = 0; i < constructors.length; i++) {
    var name = constructors[i];
    var value;
    try { value = window[name]; } catch (e) { value = null; }
    var type = typeof value;
    if (type === "undefined") {
      lines.push(name + ": undefined");
      continue;
    }
    var found = [];
    try {
      var proto = value && value.prototype;
      for (var j = 0; proto && j < methods.length; j++) {
        if (typeof proto[methods[j]] !== "undefined") found.push(methods[j]);
      }
    } catch (e2) {
      found.push("prototype-error");
    }
    lines.push(name + ": " + type + (found.length ? " [" + found.join(", ") + "]" : ""));
  }
  lines.push("navigator.getUserMedia: " + typeof navigator.getUserMedia);
  lines.push("navigator.webkitGetUserMedia: " + typeof navigator.webkitGetUserMedia);
  lines.push("navigator.mozGetUserMedia: " + typeof navigator.mozGetUserMedia);
  lines.push("navigator.mediaDevices: " + typeof navigator.mediaDevices);
  lines.push("WebSocket: " + typeof window.WebSocket);
  try {
    lines.push("WebSocket.CONNECTING: " + String(window.WebSocket && window.WebSocket.CONNECTING));
    lines.push("WebSocket.prototype: " + typeof (window.WebSocket && window.WebSocket.prototype));
  } catch (webSocketInspectError) {
    lines.push("WebSocket inspection error: " + String(webSocketInspectError));
  }
  lines.push("ArrayBuffer: " + typeof window.ArrayBuffer);
  lines.push("Uint8Array: " + typeof window.Uint8Array);
  lines.push("Promise: " + typeof window.Promise);
  lines.push("Blob: " + typeof window.Blob);
  lines.push("FileReader: " + typeof window.FileReader);
  lines.push("serviceWorker: " + typeof navigator.serviceWorker);
  return {
    user_agent: navigator.userAgent || "",
    secure_context: window.isSecureContext === true,
    lines: lines
  };
}
function showCapabilityReport() {
  var report = capabilityReport();
  var text = "User-Agent: " + report.user_agent + "\n";
  text += "Secure context: " + report.secure_context + "\n";
  text += report.lines.join("\n");
  var output = $("capabilities");
  if (output) output.value = text;
  xhr("POST", "/api/capabilities", report, function(err) {
    if (err) log("Capability report upload failed: " + err.message);
    else log("Capability report printed in the server terminal.");
  });
}
var webSocketProbeLines = [];
function publishWebSocketProbe(line) {
  webSocketProbeLines.push(line);
  log(line);
  var report = capabilityReport();
  for (var i = 0; i < webSocketProbeLines.length; i++) report.lines.push(webSocketProbeLines[i]);
  var text = "User-Agent: " + report.user_agent + "\n";
  text += "Secure context: " + report.secure_context + "\n";
  text += report.lines.join("\n");
  var output = $("capabilities");
  if (output) output.value = text;
}
function submitWebSocketProbeReport() {
  var report = capabilityReport();
  for (var i = 0; i < webSocketProbeLines.length; i++) report.lines.push(webSocketProbeLines[i]);
  xhr("POST", "/api/capabilities", report, function() {});
}
function runWebSocketProbe() {
  webSocketProbeLines = [];
  var Constructor = window.WebSocket;
  if (!Constructor) {
    publishWebSocketProbe("WS-PROBE constructor: unavailable");
    submitWebSocketProbeReport();
    return;
  }
  var scheme = location.protocol === "https:" ? "wss://" : "ws://";
  var socket;
  try {
    socket = new Constructor(scheme + location.host + "/ws-probe");
    publishWebSocketProbe("WS-PROBE constructor: success");
  } catch (err) {
    publishWebSocketProbe("WS-PROBE constructor: failed: " + (err.message || String(err)));
    submitWebSocketProbeReport();
    return;
  }
  var token = "kindle-websocket-text-" + String(new Date().getTime());
  var binarySent = false;
  var finished = false;
  var timer = setTimeout(function() {
    if (finished) return;
    finished = true;
    publishWebSocketProbe("WS-PROBE result: timeout");
    submitWebSocketProbeReport();
    try { socket.close(); } catch (ignore) {}
  }, 12000);
  socket.onopen = function() {
    publishWebSocketProbe("WS-PROBE open: success");
    publishWebSocketProbe("WS-PROBE protocol: " + (socket.protocol || "not exposed"));
    publishWebSocketProbe("WS-PROBE binaryType property: " + typeof socket.binaryType);
    try {
      socket.send(token);
      publishWebSocketProbe("WS-PROBE text send: success");
    } catch (err) {
      finish("WS-PROBE text send: failed: " + (err.message || String(err)));
    }
  };
  socket.onmessage = function(event) {
    if (typeof event.data === "string") {
      if (binarySent) {
        finish("WS-PROBE binary echo: unsupported (binary payload was coerced to text)");
        return;
      }
      if (event.data !== token) {
        finish("WS-PROBE text echo: wrong payload");
        return;
      }
      publishWebSocketProbe("WS-PROBE text echo: success");
      if (typeof window.ArrayBuffer === "undefined" || typeof window.Uint8Array === "undefined") {
        finish("WS-PROBE binary echo: skipped (typed arrays unavailable)");
        return;
      }
      try {
        socket.binaryType = "arraybuffer";
        var bytes = new Uint8Array(4);
        bytes[0] = 75; bytes[1] = 73; bytes[2] = 78; bytes[3] = 68;
        binarySent = true;
        socket.send(bytes.buffer);
        publishWebSocketProbe("WS-PROBE binary send: accepted by API");
      } catch (err) {
        binarySent = false;
        finish("WS-PROBE binary send: failed: " + (err.message || String(err)));
      }
      return;
    }
    if (binarySent) {
      var size = event.data && (event.data.byteLength || event.data.size || 0);
      finish(size === 4 ? "WS-PROBE binary echo: success" : "WS-PROBE binary echo: wrong size " + size);
    }
  };
  socket.onerror = function() {
    if (!finished) publishWebSocketProbe("WS-PROBE socket error");
  };
  socket.onclose = function(event) {
    if (!finished) finish("WS-PROBE closed before completion; code=" + String(event.code || "unknown"));
  };
  function finish(line) {
    if (finished) return;
    finished = true;
    clearTimeout(timer);
    publishWebSocketProbe(line);
    publishWebSocketProbe("WS-PROBE result: complete");
    submitWebSocketProbeReport();
    try { socket.close(); } catch (ignore) {}
  }
}
function newPeer() {
  var PC = window.RTCPeerConnection || window.webkitRTCPeerConnection || window.mozRTCPeerConnection;
  var servers = [];
  for (var i = 0; i < clientSTUNURLs.length; i++) servers.push({urls: clientSTUNURLs[i]});
  return new PC({iceServers: servers});
}
var clientSTUNURLs = [];
function loadClientConfig(cb) {
  xhr("GET", "/api/config", null, function(err, data) {
    if (!err && data.stun_urls) clientSTUNURLs = data.stun_urls;
    cb(err, data || {});
  });
}
function waitForIceComplete(pc) {
  return new Promise(function(resolve) {
    if (!pc || pc.iceGatheringState === "complete") {
      resolve();
      return;
    }
    var done = false;
    var previousCandidateHandler = pc.onicecandidate;
    var previousStateHandler = pc.onicegatheringstatechange;
    var timer = setTimeout(finish, 5000);
    pc.onicecandidate = function(ev) {
      if (previousCandidateHandler) previousCandidateHandler(ev);
      if (!ev.candidate) finish();
    };
    pc.onicegatheringstatechange = function(ev) {
      if (previousStateHandler) previousStateHandler(ev);
      if (pc.iceGatheringState === "complete") finish();
    };
    function finish() {
      if (done) return;
      done = true;
      clearTimeout(timer);
      pc.onicecandidate = previousCandidateHandler;
      pc.onicegatheringstatechange = previousStateHandler;
      resolve();
    }
  });
}
function xhr(method, url, body, cb) {
  var req = new XMLHttpRequest();
  req.open(method, url, true);
  req.onreadystatechange = function() {
    if (req.readyState !== 4) return;
    if (req.status < 200 || req.status >= 300) {
      cb(new Error(req.status + " " + req.responseText));
      return;
    }
    var data = {};
    if (req.responseText) data = JSON.parse(req.responseText);
    cb(null, data);
  };
  if (body) req.setRequestHeader("Content-Type", "application/json");
  req.send(body ? JSON.stringify(body) : null);
}
function postSignal(session, from, type, data) {
  xhr("POST", "/api/signal", {session: session, from: from, type: type, data: data}, function(err) {
    if (err) log("signal post failed: " + err.message);
  });
}
function getCompatHost() {
  return new Promise(function(resolve) {
    xhr("GET", "/api/host", null, function(err, data) {
      if (err || !data.host) {
        resolve(location.hostname);
        return;
      }
      resolve(data.host);
    });
  });
}
function rewriteLocalCandidates(desc, host) {
  var changed = 0;
  var local = 0;
  var lines = String(desc.sdp || "").split("\r\n");
  for (var i = 0; i < lines.length; i++) {
    if (lines[i].indexOf("a=candidate:") !== 0) continue;
    var parts = lines[i].split(" ");
    if (parts.length < 8) continue;
    if (parts[4] && /\.local$/i.test(parts[4])) {
      local++;
      parts[4] = host;
      lines[i] = parts.join(" ");
      changed++;
    }
  }
  if (local || changed) log("SDP mDNS host candidates: " + local + ", rewritten: " + changed + " -> " + host);
  return {type: desc.type, sdp: lines.join("\r\n")};
}
`

const senderHTML = `<!doctype html>
<html>
<head>
  <meta charset="utf-8">
  <title>Send a file</title>
  <style>
    body { font-family: sans-serif; line-height: 1.45; margin: 20px; max-width: 780px; }
    button, input { font-size: 16px; margin: 6px 0; }
    textarea { width: 100%; height: 220px; }
    .token { font-size: 42px; letter-spacing: 8px; font-weight: bold; margin: 12px 0; }
    .muted { color: #666; }
  </style>
</head>
<body>
  <h1>Send a file</h1>
  <p>Status: <strong id="support"></strong></p>
  <p><input id="file" type="file"></p>
  <p><label>Transfer mode:
    <select id="mode">
      <option value="ONLINE">Online · WebRTC P2P</option>
      <option value="R2">Offline · Cloudflare R2</option>
      <option value="SERVER_A">Offline · Server A</option>
    </select>
  </label></p>
  <p><button id="create">Create 6-digit code and send</button></p>
  <div class="token" id="token"></div>
  <p class="muted" id="hint"></p>
  <progress id="progress" value="0" max="1" style="width:100%"></progress>
  <p><a href="/http-sample.txt">HTTP sample download for comparison</a></p>
  <h2>Compatibility diagnostics</h2>
  <textarea id="capabilities" readonly></textarea>
  <textarea id="log" readonly></textarea>
<script>` + commonJS + `
var session = "";
var pc = null;
var dc = null;
var lastSeq = 0;
var selectedFile = null;
var sent = false;
var managementToken = "";
var onlineHashWorker = null;
var onlineChunkCount = 0;

$('support').innerHTML = hasWebRTC() ? "Modern RTCPeerConnection exists" : "No modern RTCPeerConnection; inspect diagnostics below";
showCapabilityReport();
$('file').onchange = function() {
  selectedFile = $('file').files[0] || null;
  if (selectedFile) log("Selected file: " + selectedFile.name + " (" + selectedFile.size + " bytes)");
};

$('create').onclick = function() {
  selectedFile = $('file').files[0] || null;
  if (!selectedFile) {
    log("Choose one file first. One token maps to one selected file.");
    return;
  }
  if (selectedFile.size > 200 * 1024 * 1024) {
    log("The first phase limit is 200 MB.");
    return;
  }
  $('create').disabled = true;
  $('file').disabled = true;
  $('mode').disabled = true;
  loadClientConfig(function(configErr, config) {
    if (configErr) return failCreate(configErr);
    var mode = $('mode').value;
    if (mode === "ONLINE") {
      if (!hasWebRTC()) return failCreate(new Error("This browser has no WebRTC API. Choose an offline mode."));
      createOnline();
      return;
    }
    if (mode === "R2" && !config.r2_enabled) return failCreate(new Error("R2 is not configured on server C."));
    if (mode === "SERVER_A" && !config.server_a_enabled) return failCreate(new Error("Server A is not configured on server C."));
    hashFile(selectedFile, function(err, sha256) {
      if (err) return failCreate(err);
      createOffline(mode, sha256);
    });
  });
};

function failCreate(err) {
  $('create').disabled = false;
  $('file').disabled = false;
  $('mode').disabled = false;
  log(err.message || String(err));
}

function createOnline() {
  xhr("POST", "/api/session", null, function(err, data) {
    if (err) {
      return failCreate(err);
    }
    session = data.id;
    managementToken = data.management_token || "";
    $('token').innerHTML = session;
    log("Token created for: " + selectedFile.name);
    log("Waiting for the receiving browser to enter the code...");
    getCompatHost().then(function(host) {
      $('hint').textContent = "On the receiving device, open http://" + host + ":" + location.port + "/ and enter this code.";
    });
    startPeer();
  });
}

function createOffline(mode, sha256) {
  log("SHA-256: " + sha256);
  xhr("POST", "/api/transfers", {
    mode: mode, filename: selectedFile.name || "download",
    content_type: selectedFile.type || "application/octet-stream",
    expected_size: selectedFile.size, sha256: sha256
  }, function(err, created) {
    if (err) return failCreate(err);
    session = created.code;
    managementToken = created.management_token;
    $('token').innerHTML = session;
    xhr("POST", "/api/transfers/" + session + "/upload-started",
      {management_token: managementToken}, function(startErr) {
      if (startErr) return failCreate(startErr);
      xhr("POST", "/api/transfers/" + session + "/upload-authorize",
        {management_token: managementToken}, function(authErr, auth) {
        if (authErr) return failCreate(authErr);
        uploadDirect(auth.upload, selectedFile, function(uploadErr, etag) {
          if (uploadErr) return failCreate(uploadErr);
          xhr("POST", "/api/transfers/" + session + "/upload-complete",
            {management_token: managementToken, etag: etag || ""}, function(doneErr) {
            if (doneErr) return failCreate(doneErr);
            $('progress').value = 1;
            $('hint').textContent = "Upload complete. The receiver can enter this code for two hours.";
            log("Offline upload ready; file bytes did not pass through server C.");
          });
        });
      });
    });
  });
}

function uploadDirect(auth, file, cb) {
  if (!auth || !auth.url || auth.method !== "PUT") return cb(new Error("Invalid upload authorization."));
  var req = new XMLHttpRequest();
  req.open("PUT", auth.url, true);
  var headers = auth.headers || {};
  for (var key in headers) if (headers.hasOwnProperty(key)) req.setRequestHeader(key, headers[key]);
  req.upload.onprogress = function(event) {
    if (event.lengthComputable) $('progress').value = event.loaded / event.total;
  };
  req.onreadystatechange = function() {
    if (req.readyState !== 4) return;
    if (req.status < 200 || req.status >= 300) return cb(new Error("Upload failed: " + req.status + " " + req.responseText));
    cb(null, req.getResponseHeader("ETag") || "");
  };
  req.onerror = function() { cb(new Error("Direct upload failed; check data-plane CORS.")); };
  req.send(file);
}

function hashFile(file, cb) {
  if (!window.Worker || !window.FileReader) return cb(new Error("This browser cannot calculate an incremental SHA-256."));
  var worker = new Worker("/sha256-worker.js");
  var reader = new FileReader(), offset = 0, chunkSize = 1024 * 1024;
  worker.onmessage = function(event) {
    var msg = event.data || {};
    if (msg.kind === "updated") {
      offset = msg.bytes;
      $('progress').value = file.size ? Math.min(1, offset / file.size) : 1;
      if (offset < file.size) readNext(); else worker.postMessage({kind:"finish"});
    } else if (msg.kind === "digest") {
      worker.terminate();
      cb(null, msg.sha256);
    }
  };
  worker.onerror = function(event) { worker.terminate(); cb(new Error(event.message || "SHA-256 worker failed")); };
  reader.onload = function(event) { worker.postMessage({kind:"chunk",buffer:event.target.result}, [event.target.result]); };
  reader.onerror = function() { worker.terminate(); cb(reader.error || new Error("File read failed")); };
  function readNext() { reader.readAsArrayBuffer(file.slice(offset, offset + chunkSize)); }
  if (file.size === 0) worker.postMessage({kind:"finish"}); else readNext();
}

function startPeer() {
  pc = newPeer();
  dc = pc.createDataChannel("file");
  dc.binaryType = "arraybuffer";
  dc.onopen = function() {
    log("DataChannel open.");
    if (managementToken) {
      xhr("POST", "/api/transfers/" + session + "/upload-started",
        {management_token: managementToken}, function(err) {
          if (err) {
            log("Online lifetime start failed; transfer not started: " + err.message);
            return;
          }
          beginOnlineSend();
        });
    } else {
      beginOnlineSend();
    }
  };
  function beginOnlineSend() {
    if (!sent) {
      sent = true;
      sendFile(selectedFile);
    }
  }
  dc.onclose = function() { log("DataChannel closed."); };
  pc.oniceconnectionstatechange = function() {
    log("ICE state: " + pc.iceConnectionState);
    if (pc.iceConnectionState === "connected" || pc.iceConnectionState === "completed") logSelectedCandidate(pc);
  };
  pc.createOffer().then(function(offer) {
    return pc.setLocalDescription(offer);
  }).then(function() {
    log("Waiting for ICE gathering to complete...");
    return waitForIceComplete(pc);
  }).then(function() {
    return getCompatHost();
  }).then(function(host) {
    var offer = rewriteLocalCandidates(pc.localDescription, host);
    postSignal(session, "sender", "offer", offer);
    log("Offer posted with embedded ICE candidates. Kindle can now join with token " + session + ".");
    poll();
  }).then(function() {
  }).catch(function(err) { log("offer failed: " + err.message); });
}

function poll() {
  if (!session) return;
  xhr("GET", "/api/poll?session=" + encodeURIComponent(session) + "&to=sender&after=" + lastSeq, null, function(err, data) {
    if (!err && data.messages) {
      for (var i = 0; i < data.messages.length; i++) handleSignal(data.messages[i]);
    }
    setTimeout(poll, 1000);
  });
}

function handleSignal(msg) {
  lastSeq = Math.max(lastSeq, msg.seq);
  if (msg.type === "answer") {
    pc.setRemoteDescription(msg.data).then(function() {
      log("Answer applied.");
    }).catch(function(err) { log("answer failed: " + err.message); });
  }
}

function sendFile(file) {
  if (!file) {
    log("No file is bound to this token.");
    return;
  }
  var negotiated = pc && pc.sctp && pc.sctp.maxMessageSize ? pc.sctp.maxMessageSize : 65536;
  var chunkSize = Math.min(32768, negotiated);
  var offset = 0;
  var reader = new FileReader();
  onlineChunkCount = 0;
  onlineHashWorker = window.Worker ? new Worker("/sha256-worker.js") : null;
  if (!onlineHashWorker) {
    log("Incremental SHA-256 worker unavailable; refusing an unverifiable Online transfer.");
    return;
  }
  var digest = null;
  onlineHashWorker.onmessage = function(event) {
    var msg = event.data || {};
    if (msg.kind === "digest") {
      digest = msg.sha256;
      dc.send(JSON.stringify({kind: "done", size: offset, chunks: onlineChunkCount, sha256: digest}));
      onlineHashWorker.terminate();
      log("Send done: " + offset + " bytes; SHA-256 " + digest);
    }
  };
  log("Sending " + file.name + "...");
  dc.send(JSON.stringify({kind: "meta", name: file.name || "webrtc-file.bin", size: file.size, mime: file.type || "application/octet-stream", chunk_size: chunkSize}));
  reader.onload = function(ev) {
    waitAndSend(ev.target.result, function() {
      onlineHashWorker.postMessage({kind:"chunk",buffer:ev.target.result});
      offset += ev.target.result.byteLength;
      onlineChunkCount++;
      $('progress').value = file.size ? offset / file.size : 1;
      if (offset < file.size) readNext();
      else {
        onlineHashWorker.postMessage({kind:"finish"});
      }
    });
  };
  function readNext() {
    reader.readAsArrayBuffer(file.slice(offset, offset + chunkSize));
  }
  function waitAndSend(buf, done) {
    var highWater = 1024 * 1024;
    if (dc.bufferedAmount > highWater) {
      dc.bufferedAmountLowThreshold = 256 * 1024;
      var resume = function() {
        dc.removeEventListener("bufferedamountlow", resume);
        waitAndSend(buf, done);
      };
      dc.addEventListener("bufferedamountlow", resume);
      return;
    }
    dc.send(buf);
    done();
  }
  if (file.size === 0) onlineHashWorker.postMessage({kind:"finish"});
  else readNext();
}

function logSelectedCandidate(peer) {
  if (!peer.getStats) return;
  peer.getStats(null).then(function(report) {
    var pairs = {}, candidates = {};
    report.forEach(function(stat) {
      if (stat.type === "candidate-pair") pairs[stat.id] = stat;
      if (stat.type === "local-candidate" || stat.type === "remote-candidate") candidates[stat.id] = stat;
    });
    for (var id in pairs) {
      var pair = pairs[id];
      if ((pair.selected || pair.nominated) && pair.state === "succeeded") {
        var local = candidates[pair.localCandidateId] || {};
        var remote = candidates[pair.remoteCandidateId] || {};
        log("Selected candidates: " + (local.candidateType || "?") + " / " + (remote.candidateType || "?"));
        if (local.candidateType === "relay" || remote.candidateType === "relay") {
          log("ERROR: relay candidate is forbidden; stopping transfer.");
          try { dc.close(); } catch (ignore) {}
        }
      }
    }
  }).catch(function(err) { log("getStats failed: " + err.message); });
}
</script>
</body>
</html>`

const receiverHTML = `<!doctype html>
<html>
<head>
  <meta charset="utf-8">
  <title>Receive a file</title>
  <style>
    body { font-family: sans-serif; line-height: 1.45; margin: 20px; max-width: 760px; }
    button, input { font-size: 20px; margin: 8px 0; }
    input { width: 8em; }
    textarea { width: 100%; height: 220px; }
    .muted { color: #666; }
  </style>
</head>
<body>
  <h1>Receive a file</h1>
  <p>Status: <strong id="support"></strong></p>
  <p>Code: <input id="session" size="6" maxlength="6" pattern="[0-9]*" inputmode="numeric"> <button id="join">Continue</button></p>
  <p><label id="direct-option"><input id="direct-save" type="checkbox"> Stream Online data directly to a chosen file when supported</label></p>
  <p class="muted">Offline codes work in Kindle's built-in browser. Online codes require WebRTC.</p>
  <progress id="progress" value="0" max="1" style="width:100%"></progress>
  <p id="result"></p>
  <p><a href="/sender">Sender page</a></p>
  <p><a href="/http-sample.txt">HTTP sample download for comparison</a></p>
  <p><button id="ws-probe">Run WebSocket capability test</button></p>
  <h2>Compatibility diagnostics</h2>
  <textarea id="capabilities" readonly></textarea>
  <textarea id="log" readonly></textarea>
<script>` + commonJS + `
var session = "";
var pc = null;
var dc = null;
var lastSeq = 0;
var chunks = [];
var meta = null;
var received = 0;
var receivedChunks = 0;
var receiveHashWorker = null;
var expectedDone = null;
var directWritable = null;
var writeQueue = null;

$('support').innerHTML = hasWebRTC() ? "Modern RTCPeerConnection exists" : "No modern RTCPeerConnection; inspect diagnostics below";
showCapabilityReport();
$('ws-probe').onclick = runWebSocketProbe;
setTimeout(runWebSocketProbe, 500);
$('session').value = (location.search.match(/[?&]id=([^&]+)/) || [])[1] || "";
initServiceWorkerDownload();
if (!window.showSaveFilePicker) $('direct-option').style.display = "none";
if ($('session').value) setTimeout(resolveCode, 100);
$('join').onclick = join;

function initServiceWorkerDownload() {
  if (!('serviceWorker' in navigator)) {
    log("Service Worker not supported; final save may fall back to Blob only.");
    return;
  }
  if (!window.isSecureContext && location.hostname !== "localhost" && location.hostname !== "127.0.0.1") {
    log("Service Worker requires HTTPS for final save. This LAN-IP page can still test WebRTC transfer.");
    return;
  }
  navigator.serviceWorker.register("/sw.js").then(function() {
    return navigator.serviceWorker.ready;
  }).then(function() {
    if (!navigator.serviceWorker.controller) {
      log("Service Worker is ready; reloading once so it can control the receiver page.");
      location.reload();
      return;
    }
    log("Service Worker controls this page; final save path is available.");
  }).catch(function(err) {
    log("Service Worker setup failed: " + (err.message || err));
  });
}

function join() {
  session = $('session').value;
  if (!/^[0-9]{6}$/.test(session)) { log("Enter a 6-digit token."); return; }
  resolveCode();
}

function resolveCode() {
  session = $('session').value;
  xhr("GET", "/api/receive/" + encodeURIComponent(session), null, function(err, data) {
    if (!err && data.download) {
      showOfflineDownload(data);
      return;
    }
    if (!hasWebRTC()) {
      log("No ready offline file was found, and this browser has no WebRTC API.");
      return;
    }
    loadClientConfig(function() { prepareDirectSaveAndJoin(); });
  });
}

function showOfflineDownload(data) {
  var size = Number(data.expected_size || 0);
  var html = "<strong>" + escapeHTML(data.filename || "download") + "</strong><br>";
  html += size + " bytes<br>SHA-256: <code>" + escapeHTML(data.sha256 || "") + "</code><br>";
  html += '<a href="' + escapeHTML(data.download.url) + '">Download file</a>';
  $('result').innerHTML = html;
  $('support').innerHTML = "Offline file ready";
  log("Offline download resolved. The download is direct from " + data.mode + ", not server C.");
}

function prepareDirectSaveAndJoin() {
  if ($('direct-save').checked && window.showSaveFilePicker) {
    window.showSaveFilePicker({suggestedName:"received-file"}).then(function(handle) {
      return handle.createWritable();
    }).then(function(writable) {
      directWritable = writable;
      writeQueue = Promise.resolve();
      joinOnline();
    }).catch(function(err) {
      log("Direct save cancelled or unavailable: " + (err.message || err));
      directWritable = null;
      joinOnline();
    });
    return;
  }
  joinOnline();
}

function joinOnline() {
  pc = newPeer();
  pc.ondatachannel = function(ev) {
    dc = ev.channel;
    dc.binaryType = "arraybuffer";
    dc.onopen = function() { log("DataChannel open."); };
    dc.onmessage = handleData;
    dc.onclose = function() { log("DataChannel closed."); };
  };
  pc.oniceconnectionstatechange = function() {
    log("ICE state: " + pc.iceConnectionState);
    if (pc.iceConnectionState === "connected" || pc.iceConnectionState === "completed") logSelectedCandidate(pc);
  };
  log("Polling for sender offer...");
  poll();
}

function poll() {
  if (!session) return;
  xhr("GET", "/api/poll?session=" + encodeURIComponent(session) + "&to=receiver&after=" + lastSeq, null, function(err, data) {
    if (!err && data.messages) {
      for (var i = 0; i < data.messages.length; i++) handleSignal(data.messages[i]);
    }
    setTimeout(poll, 1000);
  });
}

function handleSignal(msg) {
  lastSeq = Math.max(lastSeq, msg.seq);
  if (msg.type === "offer") {
    pc.setRemoteDescription(msg.data).then(function() {
      log("Offer applied.");
      return pc.createAnswer();
    }).then(function(answer) {
      return pc.setLocalDescription(answer);
    }).then(function() {
      log("Waiting for ICE gathering to complete...");
      return waitForIceComplete(pc);
    }).then(function() {
      postSignal(session, "receiver", "answer", pc.localDescription);
      log("Answer posted with embedded ICE candidates.");
    }).catch(function(err) { log("offer failed: " + err.message); });
  }
}

function handleData(ev) {
  if (typeof ev.data === "string") {
    var msg = JSON.parse(ev.data);
    if (msg.kind === "meta") {
      if (Number(msg.size) < 0 || Number(msg.size) > 200 * 1024 * 1024) {
        log("Sender declared an invalid file size; closing the channel.");
        try { dc.close(); } catch (ignore) {}
        return;
      }
      meta = msg;
      chunks = [];
      received = 0;
      receivedChunks = 0;
      expectedDone = null;
      receiveHashWorker = new Worker("/sha256-worker.js");
      receiveHashWorker.onmessage = function(event) {
        var hash = event.data || {};
        if (hash.kind === "digest") verifyAndFinish(hash.sha256);
      };
      $('progress').value = 0;
      log("Receiving " + meta.name + " (" + meta.size + " bytes)");
    } else if (msg.kind === "done") {
      expectedDone = msg;
      receiveHashWorker.postMessage({kind:"finish"});
    }
    return;
  }
  var bytes = ev.data;
  var byteLength = bytes.byteLength || bytes.size || 0;
  if (!meta || received + byteLength > Number(meta.size) || received + byteLength > 200 * 1024 * 1024) {
    log("Received more data than declared; closing the channel.");
    try { dc.close(); } catch (ignore) {}
    return;
  }
  if (!directWritable) chunks.push(bytes);
  if (directWritable) {
    writeQueue = writeQueue.then(function() { return directWritable.write(bytes); });
  }
  receiveHashWorker.postMessage({kind:"chunk",buffer:bytes});
  received += byteLength;
  receivedChunks++;
  if (meta && meta.size) $('progress').value = received / meta.size;
}

function verifyAndFinish(sha256) {
  if (!meta || !expectedDone) return;
  var failures = [];
  if (received !== Number(meta.size) || received !== Number(expectedDone.size)) failures.push("size");
  if (receivedChunks !== Number(expectedDone.chunks)) failures.push("chunk count");
  if (sha256 !== expectedDone.sha256) failures.push("SHA-256");
  if (failures.length) {
    log("Transfer verification failed: " + failures.join(", "));
    if (directWritable) writeQueue.then(function(){ return directWritable.abort(); });
    return;
  }
  if (receiveHashWorker) receiveHashWorker.terminate();
  if (directWritable) {
    writeQueue.then(function() { return directWritable.close(); }).then(function() {
      $('result').innerHTML = "Received and verified " + received + " bytes; saved directly to the chosen file.";
      log("Receive verified: " + sha256);
    }).catch(function(err) { log("Direct file close failed: " + err.message); });
    return;
  }
  finishBlobFile(sha256);
}

function finishBlobFile(sha256) {
  var type = meta && meta.mime ? meta.mime : "application/octet-stream";
  var name = meta && meta.name ? meta.name : "webrtc-file.bin";
  var blob = new Blob(chunks, {type: type});
  log("Receive verified: " + blob.size + " bytes; SHA-256 " + sha256);
  var url = URL.createObjectURL(blob);
  var html = "WebRTC received " + blob.size + " bytes. ";
  html += '<a id="download" href="' + url + '" download="' + escapeHTML(name) + '">Blob fallback link</a>';
  html += '<br><span id="sw-status">Preparing Service Worker download...</span>';
  $('result').innerHTML = html;
  prepareServiceWorkerDownload(blob, name, type);
}

function logSelectedCandidate(peer) {
  if (!peer.getStats) return;
  peer.getStats(null).then(function(report) {
    var pairs = {}, candidates = {};
    report.forEach(function(stat) {
      if (stat.type === "candidate-pair") pairs[stat.id] = stat;
      if (stat.type === "local-candidate" || stat.type === "remote-candidate") candidates[stat.id] = stat;
    });
    for (var id in pairs) {
      var pair = pairs[id];
      if ((pair.selected || pair.nominated) && pair.state === "succeeded") {
        var local = candidates[pair.localCandidateId] || {};
        var remote = candidates[pair.remoteCandidateId] || {};
        log("Selected candidates: " + (local.candidateType || "?") + " / " + (remote.candidateType || "?"));
        if (local.candidateType === "relay" || remote.candidateType === "relay") {
          log("ERROR: relay candidate is forbidden.");
          try { dc.close(); } catch (ignore) {}
        }
      }
    }
  }).catch(function(err) { log("getStats failed: " + err.message); });
}

function prepareServiceWorkerDownload(blob, name, type) {
  var status = $('sw-status');
  if (!('serviceWorker' in navigator)) {
    status.innerHTML = "Service Worker is not supported here; Blob fallback is the only browser path.";
    log("Service Worker not supported.");
    return;
  }
  if (!window.isSecureContext && location.hostname !== "localhost" && location.hostname !== "127.0.0.1") {
    status.innerHTML = "Service Worker needs HTTPS. This HTTP LAN-IP demo cannot test final save.";
    log("Service Worker unavailable on non-HTTPS LAN IP.");
    return;
  }
  var id = String(Date.now()) + "-" + Math.floor(Math.random() * 1000000);
  navigator.serviceWorker.register("/sw.js").then(function(reg) {
    log("Service Worker registered.");
    return navigator.serviceWorker.ready;
  }).then(function(reg) {
    if (!navigator.serviceWorker.controller) {
      return Promise.reject(new Error("Service Worker is not controlling this page yet. Reload first, then receive again."));
    }
    return blobToArrayBuffer(blob);
  }).then(function(buffer) {
    return storeFileInServiceWorker(id, name, type, buffer);
  }).then(function() {
    var href = "/sw-download/" + encodeURIComponent(id);
    status.innerHTML = '<a href="' + href + '">Save via Service Worker HTTP response</a>';
    log("Service Worker download URL prepared: " + href);
    location.href = href;
  }).catch(function(err) {
    status.innerHTML = "Service Worker download failed: " + escapeHTML(err.message || String(err));
    log("Service Worker download failed: " + (err.message || err));
  });
}

function blobToArrayBuffer(blob) {
  if (blob.arrayBuffer) return blob.arrayBuffer();
  if (window.Response) return new Response(blob).arrayBuffer();
  return new Promise(function(resolve, reject) {
    var reader = new FileReader();
    reader.onload = function() { resolve(reader.result); };
    reader.onerror = function() { reject(reader.error || new Error("FileReader failed")); };
    reader.readAsArrayBuffer(blob);
  });
}

function storeFileInServiceWorker(id, name, type, buffer) {
  return new Promise(function(resolve, reject) {
    var channel = new MessageChannel();
    var timer = setTimeout(function() {
      reject(new Error("Service Worker did not acknowledge the file"));
    }, 10000);
    channel.port1.onmessage = function(ev) {
      clearTimeout(timer);
      if (ev.data && ev.data.ok) resolve();
      else reject(new Error(ev.data && ev.data.error ? ev.data.error : "Service Worker rejected the file"));
    };
    navigator.serviceWorker.controller.postMessage({
      kind: "store-file",
      id: id,
      name: name,
      type: type,
      buffer: buffer
    }, [channel.port2, buffer]);
  });
}

function escapeHTML(s) {
  return String(s).replace(/[&<>"']/g, function(c) {
    return {"&":"&amp;","<":"&lt;",">":"&gt;","\"":"&quot;","'":"&#39;"}[c];
  });
}
</script>
</body>
</html>`

const serviceWorkerJS = `var files = new Map();

self.addEventListener("install", function(event) {
  self.skipWaiting();
});

self.addEventListener("activate", function(event) {
  event.waitUntil(self.clients.claim());
});

self.addEventListener("message", function(event) {
  var data = event.data || {};
  if (data.kind !== "store-file") return;
  try {
    files.set(data.id, {
      name: data.name || "webrtc-file.bin",
      type: data.type || "application/octet-stream",
      buffer: data.buffer
    });
    event.ports[0].postMessage({ok: true});
  } catch (err) {
    event.ports[0].postMessage({ok: false, error: err.message || String(err)});
  }
});

self.addEventListener("fetch", function(event) {
  var url = new URL(event.request.url);
  if (url.pathname.indexOf("/sw-download/") !== 0) return;
  var id = decodeURIComponent(url.pathname.substring("/sw-download/".length));
  var file = files.get(id);
  if (!file) {
    event.respondWith(new Response("File token not found in Service Worker", {
      status: 404,
      headers: {"Content-Type": "text/plain; charset=utf-8"}
    }));
    return;
  }
  files.delete(id);
  event.respondWith(new Response(file.buffer, {
    status: 200,
    headers: {
      "Content-Type": file.type,
      "Content-Length": String(file.buffer.byteLength || 0),
      "Content-Disposition": "attachment; filename*=UTF-8''" + encodeRFC5987(file.name)
    }
  }));
});

function encodeRFC5987(value) {
  return encodeURIComponent(value).replace(/['()*]/g, function(c) {
    return "%" + c.charCodeAt(0).toString(16).toUpperCase();
  });
}
`
