package main

import (
	"log"
	"mime"
	"net/http"
	"path"
	"runtime"
)

// selectContentType - parse file extension and determine content type
func selectContentType(filename string) string {
	extension := path.Ext(filename)
	if extension == "" {
		// return default if no file extension is found
		return "application/octet-stream"
	}
	return mime.TypeByExtension(extension)
}

// cancelRequestIfUnhealthy - action not possible due to broken backend
func cancelRequestIfUnhealthy(w http.ResponseWriter) bool {
	if backendState != StateHealthy {
		w.WriteHeader(http.StatusServiceUnavailable)
		return true
	}
	return false
}

// traceLog - logs msg to logger with detailed information about location in code. If logger is set to nil, the default logger will be used
func traceLog(logger *log.Logger, msg interface{}) {
	if logger == nil {
		logger = log.Default()
	}
	pc, file, line, ok := runtime.Caller(1)
	details := runtime.FuncForPC(pc)

	if ok && details != nil {
		logger.Printf("%v @ %v:%d | %v", details.Name(), path.Base(file), line, msg)
	}
}
