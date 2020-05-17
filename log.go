package main

// structured logger

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"time"
)

type LogLevel int

const (
	NOLOG LogLevel = iota
	CRITICAL
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

type LogMessage struct {
	// the level at which this message should be logged
	Level LogLevel

	// structured log data - all keys/values will be output
	Context LogContext

	// This function can run arbitrary code in a callback IFF the log is turned up to 'DEBUG'
	// use this for heavyweight debugging code that you only want to run under special circumstances
	DebugDetails func() string
}

// 0 value will disable logging
// the main logger is for diagnostics and debug logging
var Logger logger

// this logger is for query logging
var QueryLogger logger

// performs a sprintf with a given format string and arguments iff the message is printable
// at the logger's current level, this allows flexible log messages that can be turned on and
// off easily without performing expensive sprintfs
func (l logger) Sprintf(level LogLevel, format string, args ...interface{}) string {
	if level <= l.level {
		return fmt.Sprintf(format, args...)
	}
	return "[message suppressed by log system]"
}

// takes a structured message, checks log level, outputs it in a set format
func (l logger) Log(message LogMessage) {
	if l.handle == nil {
		// this logger was never initialized, just bail
		return
	}
	if message.Level <= l.level {
		message.Context["level"] = levelToString(message.Level)
		now := time.Now()
		message.Context["when"] = fmt.Sprintf("%d-%02d-%02d:%02d:%02d:%02d", now.Year(), now.Month(), now.Day(), now.Hour(), now.Minute(), now.Second())
		output := parseKeys(message.Context)
		if l.level >= DEBUG && message.DebugDetails != nil {
			message.Context["debug"] = message.DebugDetails()
		}
		l.output(output)
	}
}

func (l logger) output(output string) {
	fmt.Fprintf(l.handle, "%s\n", output)
}


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
func NewLogMessage(level LogLevel, context LogContext, debugDetails func() string) LogMessage {
	return LogMessage{
		Level:        level,
		Context:      context,
		DebugDetails: debugDetails,
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

	l.Log(LogMessage{
		Level: INFO,
		Context: LogContext{
			"what":   "initialized new query logger",
			"logger": Logger.Sprintf(DEBUG, "%v", QueryLogger),
		},
	})

	return nil
}
