package main

import (
	"fmt"
	"net"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"time"

	log "github.com/sirupsen/logrus"
	open "github.com/skratchdot/open-golang/open"
	flag "github.com/spf13/pflag"
)

// Mux is the http servemux for this server
type Mux struct {
	mux *http.ServeMux
}

// NewMux creates a new mux instance
func NewMux() *Mux {
	return &Mux{
		mux: http.NewServeMux(),
	}
}

// Handle registers the handler for the given pattern
func (mux *Mux) Handle(pattern string, handler http.Handler) {
	mux.mux.Handle(pattern, handler)
}

// HandleFunc registers the handler func for the given pattern
func (mux *Mux) HandleFunc(pattern string, handler func(http.ResponseWriter, *http.Request)) {
	mux.mux.HandleFunc(pattern, handler)
}

// ServeHTTP dispatches the request to the handler whose pattern most closely matches the request URL
func (mux *Mux) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	log.WithFields(log.Fields{
		"remoteAddr": r.RemoteAddr,
		"userAgent":  r.UserAgent(),
		"requestURI": r.RequestURI,
		"method":     r.Method,
	}).Debug()
	mux.mux.ServeHTTP(w, r)
}

func main() {
	// only output >= info by default
	log.SetLevel(log.InfoLevel)

	// get good timestamps for logs
	log.SetFormatter(&log.TextFormatter{
		FullTimestamp: true,
	})

	// get the path relative to the executble
	recipePath, err := os.Executable()
	if err != nil {
		log.WithError(err).Errorln("Failed to get executable path")
		recipePath = ""
	}

	debug := false
	listenAddr := "127.0.0.1"
	listenPort := uint16(0)
	indexPath := filepath.Join(path.Dir(recipePath), "search_idx")
	recipePath = filepath.Join(path.Dir(recipePath), "Recipes")
	recipePath, err = filepath.Abs(recipePath)
	if err != nil {
		log.WithError(err).Errorln("Failed to get absolute path for default recipe path")
		recipePath = ""
	}
	indexPath, err = filepath.Abs(indexPath)
	if err != nil {
		log.WithError(err).Errorln("Failed to get absolute path for default index path")
		indexPath = ""
	}

	flag.StringVarP(&listenAddr, "host", "h", listenAddr, "HTTP listen address")
	flag.Uint16VarP(&listenPort, "port", "p", listenPort, "HTTP listen port")
	flag.StringVarP(&recipePath, "recipes", "r", recipePath, "Path to recipes")
	flag.StringVarP(&indexPath, "index", "i", indexPath, "Path for search index")
	flag.BoolVarP(&debug, "debug", "d", debug, "Enable debug mode")
	flag.Parse()

	if debug {
		log.SetLevel(log.DebugLevel)
	}

	log.WithFields(log.Fields{
		"host":    listenAddr,
		"port":    listenPort,
		"recipes": recipePath,
		"index":   indexPath,
		"debug":   debug,
	}).Debugln("Options received")

	log.Debugln("Creating new handler")
	handler, err := NewHandler(recipePath, indexPath, log.StandardLogger())
	if err != nil {
		log.WithError(err).Errorln("Failed to create new handler")
		os.Exit(1)
	}

	log.Debugln("Successfully created new handler")

	defer handler.Close()

	log.Debugln("Creating TCP listening port")
	listener, err := net.Listen("tcp", fmt.Sprintf("%s:%d", listenAddr, listenPort))
	if err != nil {
		log.WithError(err).Errorln("Failed to listen on new TCP port")
		os.Exit(1)
	}

	defer listener.Close()

	mux := NewMux()

	log.Debugln("Associating handler funcs with http server")
	for pattern, handlerFunc := range handler.GetHandlerFuncs() {
		mux.HandleFunc(pattern, handlerFunc)
	}

	go func() {
		log.Infoln("Waiting before opening web browser")
		time.Sleep(time.Second)
		url := "http://" + listener.Addr().String()
		err = open.Run(url)
		if err == nil {
			log.Infoln("Opened", url, "in your web browser")
		} else {
			log.Infoln("Open", url, "in your web browser")
		}
	}()

	err = http.Serve(listener, mux)
	if err != nil {
		log.WithError(err).Fatalln("HTTP server died")
	}
}
