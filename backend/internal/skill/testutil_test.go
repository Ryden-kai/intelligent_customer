// Tiny shim around os.WriteFile so the test file doesn't need an extra
// import when other tests in the same package use it.

package skill_test

import "os"

func osWriteFile(path string, b []byte, mode os.FileMode) error {
	return os.WriteFile(path, b, mode)
}