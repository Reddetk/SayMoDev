package logger

import "os"

func writeStdout(p []byte) (int, error) { return os.Stdout.Write(p) }
