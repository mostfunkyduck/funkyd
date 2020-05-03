package main

// logging wrapper implementing https://www.usenix.org/system/files/login/articles/login_summer19_07_legaza.pdf
import (
	"fmt"
	"io"
	"os"
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
	level      LogLevel
	handle     io.Writer
	alwaysTrim bool
}

type logMessage struct {
	Level LogLevel

	// What happened?
	What string

	// Why did this happen?
	Why string

	// What do we do next?
	Next string

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

// takes a structured message, outputs in trimmed format (may make parsing more complicated, but is less verbose)
func (l logger) LogTrimmed(message logMessage) error {
	if message.Level <= l.level {
		output := ""
		if message.What != "" {
			output = fmt.Sprintf("%s [%s]", output, message.What)
		}

		if message.Why != "" {
			output = fmt.Sprintf("%s [%s]", output, message.Why)
		}

		if message.Next != "" {
			output = fmt.Sprintf("%s [%s]", output, message.Next)
		}

		if l.level == DEBUG && message.DebugDetails != "" {
			output = fmt.Sprintf("%s [%s]", output, message.DebugDetails)
		}

		l.output(output)
	}
	return nil
}

// takes a structured message, checks log level, outputs it in a set format
func (l logger) Log(message logMessage) error {
	if l.alwaysTrim {
		return l.LogTrimmed(message)
	}

	if message.Level <= l.level {
		output := fmt.Sprintf("[%s] [%s] [%s] [%s]",
			levelToString(message.Level),
			message.What,
			message.Why,
			message.Next)
		if l.level == DEBUG {
			output = fmt.Sprintf("%s [%s]", output, message.DebugDetails)
		} else {
			output = fmt.Sprintf("%s []", output)
		}
		l.output(output)
	}
	return nil
}

func (l *logger) output(output string) {
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
func NewLogMessage(level LogLevel, what string, why string, next string, debugDetails string) logMessage {
	return logMessage{
		Level:        level,
		What:         what,
		Next:         next,
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
		level:      config.ServerLog.Level,
		handle:     handle,
		alwaysTrim: config.ServerLog.TrimFormat,
	}

	l.Log(NewLogMessage(
		INFO,
		fmt.Sprintf("initialized new server logger at level [%s]", levelToString(l.level)),
		"",
		"",
		fmt.Sprintf("%v", l),
	))
	Logger = l

	handle, err = getLoggerHandle(config.QueryLog.Location)
	if err != nil {
		return fmt.Errorf("error opening query log file [%s]: [%s]", config.QueryLog.Location, err)
	}
	QueryLogger = logger{
		level:      DEBUG,
		handle:     handle,
		alwaysTrim: config.QueryLog.TrimFormat,
	}

	l.Log(NewLogMessage(
		INFO,
		"initialized new query logger",
		"",
		"",
		fmt.Sprintf("%v", QueryLogger),
	))

	return nil
}
