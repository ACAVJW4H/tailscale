// Copyright (c) 2021 Tailscale Inc & AUTHORS All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package controlhttp

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"

	"tailscale.com/control/controlbase"
	"tailscale.com/types/key"
)

// AcceptHTTP upgrades the HTTP request given by w and r into a
// Tailscale control protocol base transport connection.
//
// AcceptHTTP always writes an HTTP response to w. The caller must not
// attempt their own response after calling AcceptHTTP.
func AcceptHTTP(ctx context.Context, w http.ResponseWriter, r *http.Request, private key.MachinePrivate) (*controlbase.Conn, error) {
	log.Println("XXXX AcceptHTTP")
	next := r.Header.Get("Upgrade")
	if next == "" {
		http.Error(w, "missing next protocol", http.StatusBadRequest)
		return nil, errors.New("no next protocol in HTTP request")
	}
	if next != upgradeHeader {
		http.Error(w, "unknown next protocol", http.StatusBadRequest)
		return nil, fmt.Errorf("client requested unhandled next protocol %q", next)
	}
	log.Println("XXXX After upgrade")

	init, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, "error reading request", http.StatusInternalServerError)
		return nil, fmt.Errorf("reading client request body: %v", err)
	}
	log.Println("XXXX After init")

	hijacker, ok := w.(http.Hijacker)
	if !ok {
		http.Error(w, "make request over HTTP/1", http.StatusBadRequest)
		return nil, errors.New("can't hijack client connection")
	}
	log.Println("XXXX After hijack cast")

	w.Header().Set("Upgrade", upgradeHeader)
	w.Header().Set("Connection", "upgrade")
	w.WriteHeader(http.StatusSwitchingProtocols)

	log.Println("XXXX After 101")

	conn, brw, err := hijacker.Hijack()
	if err != nil {
		return nil, fmt.Errorf("hijacking client connection: %w", err)
	}
	log.Println("XXXX After hijack")
	if err := brw.Flush(); err != nil {
		conn.Close()
		return nil, fmt.Errorf("flushing hijacked HTTP buffer: %w", err)
	}
	log.Println("XXXX After flush")
	if brw.Reader.Buffered() > 0 {
		conn = &drainBufConn{conn, brw.Reader}
	}

	log.Printf("XXXX WTF is this %T", conn)

	nc, err := controlbase.Server(ctx, conn, private, init)
	if err != nil {
		conn.Close()
		return nil, fmt.Errorf("noise handshake failed: %w", err)
	}
	log.Println("XXXX After noise")

	return nc, nil
}

// drainBufConn is a net.Conn with an initial bunch of bytes in a
// bufio.Reader. Read drains the bufio.Reader until empty, then passes
// through subsequent reads to the Conn directly.
type drainBufConn struct {
	net.Conn
	r *bufio.Reader
}

func (b *drainBufConn) Read(bs []byte) (int, error) {
	if b.r == nil {
		return b.Conn.Read(bs)
	}
	n, err := b.r.Read(bs)
	if b.r.Buffered() == 0 {
		b.r = nil
	}
	return n, err
}
