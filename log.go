package main

// logging wrapper implementing https://www.usenix.org/system/files/login/articles/login_summer19_07_legaza.pdf
import (
	"fmt"
	"log"
)

const (
	NOLOG LogLevel = iota
	CRITICAL
	ERROR
	WARNING
	INFO
	DEBUG
)

type logger struct {
	level LogLevel
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

// takes a structured message, checks log level, outputs it in a set format
func (l logger) Log(message logMessage) error {
	if message.Level <= l.level {
		output := fmt.Sprintf("[%s] [%s] [%s] [%s]",
			levelToString(message.Level),
			message.What,
			message.Why,
			message.Next)
		if l.level == DEBUG {
			output = fmt.Sprintf("%s [%s]", output, message.DebugDetails)
		}
		// this prevents external code from messing with our logging
		// also outputs file location
		log.SetFlags(log.Lshortfile | log.LstdFlags)
		log.Println(output)
	}
	return nil
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

// initializes a logger
func InitLogger(level LogLevel) {
	l := logger{
		level: level,
	}
	l.Log(NewLogMessage(
		INFO,
		fmt.Sprintf("initialized new main logger at level [%s]", levelToString(level)),
		"",
		"",
		fmt.Sprintf("%v", l),
	))
	Logger = l

	// this guy will ALWAYS log until i implement special options to disable query logging
	QueryLogger = logger {
		level: DEBUG,
	}
	l.Log(NewLogMessage(
		INFO,
		"initialized new query logger",
		"",
		"",
		fmt.Sprintf("%v", QueryLogger),
	))
}
