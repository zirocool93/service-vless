package tunnel

import (
	"context"
	"crypto/rand"
	"encoding/binary"
	"errors"
	"io"
	"net"
	"time"
)

func dnsProbe(ctx context.Context, network string, kind uint16) error {
	q := make([]byte, 12)
	_, _ = rand.Read(q[:2])
	q[2] = 1
	q[5] = 1
	q = append(q, 7)
	q = append(q, []byte("example")...)
	q = append(q, 3)
	q = append(q, []byte("com")...)
	q = append(q, 0, byte(kind>>8), byte(kind), 0, 1)
	d := net.Dialer{Timeout: 4 * time.Second}
	c, e := d.DialContext(ctx, network+"4", "1.1.1.1:53")
	if e != nil {
		return e
	}
	defer c.Close()
	deadline := time.Now().Add(5 * time.Second)
	if end, ok := ctx.Deadline(); ok && end.Before(deadline) {
		deadline = end
	}
	_ = c.SetDeadline(deadline)
	var answer []byte
	if network == "tcp" {
		length := []byte{byte(len(q) >> 8), byte(len(q))}
		if _, e = c.Write(append(length, q...)); e != nil {
			return e
		}
		if _, e = io.ReadFull(c, length); e != nil {
			return e
		}
		n := int(binary.BigEndian.Uint16(length))
		if n < 12 || n > 4096 {
			return errors.New("Некорректная длина DNS")
		}
		answer = make([]byte, n)
		_, e = io.ReadFull(c, answer)
	} else {
		if _, e = c.Write(q); e != nil {
			return e
		}
		answer = make([]byte, 4096)
		var n int
		n, e = c.Read(answer)
		answer = answer[:n]
	}
	if e != nil {
		return e
	}
	if len(answer) < 12 || answer[0] != q[0] || answer[1] != q[1] || answer[2]&0x80 == 0 || answer[3]&0xf != 0 || binary.BigEndian.Uint16(answer[6:8]) == 0 {
		return errors.New("DNS ответ не подтверждён")
	}
	return nil
}
