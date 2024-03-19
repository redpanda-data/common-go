package interceptor

import (
	"net/http"

	"connectrpc.com/connect"
)

var _ connect.StreamingHandlerConn = (*streamingHandlerConn)(nil)

type streamingHandlerConn struct {
	base connect.StreamingHandlerConn
	rm   *RequestMetadata
}

func (c streamingHandlerConn) Spec() connect.Spec {
	return c.base.Spec()
}

func (c streamingHandlerConn) Peer() connect.Peer {
	return c.base.Peer()
}

func (c streamingHandlerConn) RequestHeader() http.Header {
	return c.base.RequestHeader()
}

func (c streamingHandlerConn) ResponseHeader() http.Header {
	return c.base.ResponseHeader()
}

func (c streamingHandlerConn) ResponseTrailer() http.Header {
	return c.base.ResponseTrailer()
}

func (c streamingHandlerConn) Receive(m any) error {
	if err := c.base.Receive(m); err != nil {
		return err
	}
	c.rm.messagesReceived++
	return nil
}

func (c streamingHandlerConn) Send(m any) error {
	if err := c.base.Send(m); err != nil {
		return err
	}
	c.rm.messagesSent++
	return nil
}
