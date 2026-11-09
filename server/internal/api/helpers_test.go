package api_test

import "os"

func osWrite(path, s string) error { return os.WriteFile(path, []byte(s), 0o600) }
