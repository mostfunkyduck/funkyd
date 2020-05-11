package main

// structured logger
import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"time"
)

const (
	NOLOG    LogLevel = iota
	CRITICAL          // the paper discourages using this, i'm leaving it in to meet expectations and to allow unconditional logging
	ERROR
	WARNING
	INFO
	DEBUG
)

type logger struct {
	level  LogLevel
	handle io.Writer
}

type LogContext map[string]string
type logMessage struct {
	Level LogLevel

	Context LogContext
	// Verbose details
	DebugDetails string
}

// 0 value will disable logging
// the main logger is for diagnostics and debug logging
var Logger logger

// this logger is for query logging
var QueryLogger logger

func (l logger) SetLevel(level LogLevel) {
	l.level = level
}

func parseKeys(context map[string]string) string {
	output, err := json.Marshal(context)
	if err != nil {
		return fmt.Sprintf("could not marshal [%v] to JSON", context)
	}
	return string(output)
}

// takes a structured message, checks log level, outputs it in a set format
func (l logger) Log(message logMessage) {
	if l.handle == nil {
		// this logger was never initialized, just bail
		return
	}
	if message.Level <= l.level {
		message.Context["level"] = levelToString(message.Level)
		message.Context["when"] = fmt.Sprintf("%s", time.Now())
		output := parseKeys(message.Context)
		l.output(output)
	}
}

func (l logger) output(output string) {
	fmt.Fprintf(l.handle, "%s\n", output)
}

func levelToString(level LogLevel) string {
	switch level {
	case CRITICAL:
		return "CRITICAL"
	case ERROR:
		return "ERROR"
	case WARNING:
		return "WARNING"
	case INFO:
		return "INFO"
	case DEBUG:
		return "DEBUG"
	}
	return "UNDEFINED"
}

// constructor, enforces format
func NewLogMessage(level LogLevel, context LogContext, debugDetails func() string) logMessage {
	if level == DEBUG && debugDetails != nil {
		context["debug"] = debugDetails()
	}
	return logMessage{
		Level:   level,
		Context: context,
	}
}

// helper function to open a file to log to
// default behavior is to open 'location', other options include:
// "": /dev/null
// "/dev/stderr": os.Stderr
// "/dev/stdout": os.Stdout
func getLoggerHandle(location string) (*os.File, error) {
	var handle *os.File
	var err error
	switch location {
	case "":
		handle, err = os.Open(os.DevNull)
	case "/dev/stderr":
		handle = os.Stderr
	case "/dev/stdout":
		handle = os.Stdout
	default:
		handle, err = os.OpenFile(location, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	}
	if err != nil {
		return &os.File{}, fmt.Errorf("could not query logging file [%s]: %s", location, err)
	}
	return handle, nil
}

// initializes loggers
func InitLoggers() error {
	config := GetConfiguration()
	handle, err := getLoggerHandle(config.ServerLog.Location)
	if err != nil {
		return fmt.Errorf("could not open server log location [%s]: [%s]", config.ServerLog.Location, err)
	}
	l := logger{
		level:  config.ServerLog.Level,
		handle: handle,
	}

	l.Log(NewLogMessage(
		INFO,
		LogContext{
			"what": fmt.Sprintf("initialized new server logger at level [%s]", levelToString(l.level)),
		},
		func() string { return fmt.Sprintf("%v", l) },
	))
	Logger = l

	handle, err = getLoggerHandle(config.QueryLog.Location)
	if err != nil {
		return fmt.Errorf("error opening query log file [%s]: [%s]", config.QueryLog.Location, err)
	}
	QueryLogger = logger{
		level:  DEBUG,
		handle: handle,
	}

	l.Log(NewLogMessage(
		INFO,
		LogContext{
			"what": "initialized new query logger",
		},
		func() string { return fmt.Sprintf("%v", QueryLogger) },
	))

	return nil
}
