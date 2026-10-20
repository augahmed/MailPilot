//go:build !darwin && !linux

package main

func peakRSS() int64 { return 0 }
