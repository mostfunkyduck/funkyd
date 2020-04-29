package main
// logging wrapper implementing https://www.usenix.org/system/files/login/articles/login_summer19_07_legaza.pdf
import (
  "log"
  "fmt"
)

type LogLevel int
const (
  NOLOG    LogLevel = iota
  CRITICAL
  ERROR
  WARNING
  INFO
  DEBUG
)

type Logger struct {
  Level LogLevel
}

type logMessage struct {
  Level LogLevel

  // What happened?
  What  string

  // Why did this happen?
  Why   string

  // What do we do next?
  Next  string

  // Verbose details
  DebugDetails string
}

// initializes a logger
func InitLogger(level LogLevel) Logger {
  l := Logger{
    Level: level,
  }
  l.Log(NewLogMessage(
    INFO,
    fmt.Sprintf("initialized new logger at level [%s]", levelToString(level)),
    "",
    "",
    fmt.Sprintf("%v",l),
  ))
  return l
}

// takes a structured message, checks log level, outputs it in a set format
func (l Logger) Log(message logMessage) error {
  if message.Level <= l.Level {
    output := fmt.Sprintf("[%s] [%s] [%s] [%s]",
                          levelToString(message.Level),
                          message.What,
                          message.Why,
                          message.Next)
    if message.Level == DEBUG {
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
    case CRITICAL: return "CRITICAL"
    case ERROR:    return "ERROR"
    case WARNING:  return "WARNING"
    case INFO:     return "INFO"
    case DEBUG:    return "DEBUG"
  }
  return "UNDEFINED"
}

// constructor, enforces format
func NewLogMessage(level LogLevel, what string, why string, next string, debugDetails string) logMessage {
  return logMessage {
    Level: level,
    What: what,
    Next: next,
    DebugDetails: debugDetails,
  }
}

