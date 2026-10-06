//go:build linux

package tunnel

import (
	"errors"
	"os"
	"syscall"
	"time"
)

func fileLock(path string) (func(), error) {
	f, e := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0600)
	if e != nil {
		return nil, e
	}
	deadline := time.Now().Add(10 * time.Second)
	for {
		e = syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
		if e == nil {
			break
		}
		if !errors.Is(e, syscall.EWOULDBLOCK) && !errors.Is(e, syscall.EAGAIN) {
			f.Close()
			return nil, e
		}
		if time.Now().After(deadline) {
			f.Close()
			return nil, errors.New("Истекло время ожидания блокировки сетевой транзакции")
		}
		time.Sleep(100 * time.Millisecond)
	}
	return func() { _ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN); _ = f.Close() }, nil
}

func installGuard() (func(), error) {
	f, e := os.Open("/run/lock/ubuntu-vpn-gateway-install.lock")
	if e != nil {
		return nil, e
	}
	if e = syscall.Flock(int(f.Fd()), syscall.LOCK_SH|syscall.LOCK_NB); e != nil {
		f.Close()
		return nil, errors.New("Установка или обновление уже выполняется")
	}
	return func() { _ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN); _ = f.Close() }, nil
}
