package jev

import "os"

func writeFileAtomic(path string, body []byte) error {
	return os.WriteFile(path, body, 0o644)
}
