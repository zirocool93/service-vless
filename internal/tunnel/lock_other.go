//go:build !linux

package tunnel

import "errors"

func fileLock(string) (func(), error) {
	return nil, errors.New("Full Tunnel поддерживается только в Linux")
}
func installGuard() (func(), error) {
	return nil, errors.New("Full Tunnel поддерживается только в Linux")
}
