package main

type Server struct{
  ACache *RecordCache
}

type Record struct {
  Label  string
  // maps to values from the dns library
  Rrtype uint16
  Ttl    int
  Value  string
}


