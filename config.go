package main

import (
    "encoding/json"
    "os"
    "fmt"
)

type Configuration struct {
  ZoneFiles []string `json:"zonefiles"`
  Port      int      `json:"port"`
}

func NewConfiguration(configpath string) (Configuration, error) {
  file, _ := os.Open(configpath)
  defer file.Close()
  decoder := json.NewDecoder(file)
  configuration := Configuration{}
  err := decoder.Decode(&configuration)
  if err != nil {
    return Configuration{}, fmt.Errorf("error while loading configuration from JSON: %s\n", err)
  }
  return configuration, nil
}
