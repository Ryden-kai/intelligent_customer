package industry

import "os"

func osWriteFile(path string, body []byte) error {
	return os.WriteFile(path, body, 0o644)
}
