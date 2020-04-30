package main

import (
  "encoding/json"
  "fmt"
  "log"
  "os"
)

type Configuration struct {
  // Location of zone files with local dns configuration
  ZoneFiles []string `json:"zonefiles"`

  // Port to listen for DNS traffic on
  DnsPort int `json:"dnsport"`

  // List of upstream resolvers, overrides resolv.conf
  Resolvers []string `json:"resolvers"`

  // Port to expose admin API on
  HttpPort int `json:"httpport"`

  // Overrides log level
  Level LogLevel  `json:"loglevel"`
}

var configuration Configuration

func InitConfiguration(configpath string) error {
  file, _ := os.Open(configpath)
  defer file.Close()
  decoder := json.NewDecoder(file)
  configuration = Configuration{}
  err := decoder.Decode(&configuration)
  log.Printf("%v\n", configuration)
  if err != nil {
    return fmt.Errorf("error while loading configuration from JSON: %s\n", err)
  }
  return nil
}

func GetConfiguration() Configuration {
  return configuration
}
