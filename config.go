package main

import (
    "encoding/json"
    "os"
    "fmt"
)

type Configuration struct {
  ZoneFiles []string `json:"zonefiles"`
  Port      int      `json:"port"`
  Resolvers []string `json:"resolvers"`
}

var configuration = Configuration{}

func InitConfiguration(configpath string) error {
  file, _ := os.Open(configpath)
  defer file.Close()
  decoder := json.NewDecoder(file)
  configuration := Configuration{}
  err := decoder.Decode(&configuration)
  if err != nil {
    return fmt.Errorf("error while loading configuration from JSON: %s\n", err)
  }
  return nil
}

func GetConfiguration() Configuration {
  return configuration
}
