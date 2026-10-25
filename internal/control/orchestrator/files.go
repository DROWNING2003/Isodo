package orchestrator

import (
	"bytes"
	"context"
	"errors"
	guestv1 "github.com/DROWNING2003/Isodo/internal/api/guest/v1"
	nodev1 "github.com/DROWNING2003/Isodo/internal/api/node/v1"
	"github.com/DROWNING2003/Isodo/internal/sandboxfiles"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"io"
)

func (c *Client) Write(parent context.Context, header *nodev1.RuntimeWriteHeader, source io.Reader) (*guestv1.WriteFileResponse, error) {
	if _, err := c.GetNodeStatus(parent); err != nil {
		return nil, err
	}
	ctx, cancel := context.WithCancel(parent)
	defer cancel()
	stream, err := c.sandboxRuntime.WriteFile(ctx)
	if err != nil {
		return nil, err
	}
	send := func(frame *nodev1.RuntimeWriteFrame) error {
		if err := stream.Send(frame); err != nil {
			if _, replyErr := stream.CloseAndRecv(); replyErr != nil {
				return replyErr
			}
			return err
		}
		return nil
	}
	if err := send(&nodev1.RuntimeWriteFrame{Frame: &nodev1.RuntimeWriteFrame_Header{Header: header}}); err != nil {
		return nil, err
	}
	buffer := make([]byte, sandboxfiles.ChunkBytes)
	var offset uint64
	for {
		n, readErr := source.Read(buffer)
		if n > 0 {
			if err := send(&nodev1.RuntimeWriteFrame{Frame: &nodev1.RuntimeWriteFrame_Chunk{Chunk: &guestv1.FileChunk{Offset: offset, Data: bytes.Clone(buffer[:n])}}}); err != nil {
				return nil, err
			}
			offset += uint64(n)
		}
		if errors.Is(readErr, io.EOF) {
			break
		}
		if readErr != nil {
			return nil, readErr
		}
	}
	result, err := stream.CloseAndRecv()
	if err != nil && (status.Code(err) == codes.Unavailable || status.Code(err) == codes.Canceled || status.Code(err) == codes.DeadlineExceeded) {
		return nil, status.Error(codes.Unknown, "COMMIT_UNKNOWN: verify the target file before retrying")
	}
	return result, err
}
func (c *Client) Read(ctx context.Context, request *nodev1.RuntimeReadRequest, consume func(*guestv1.ReadFileFrame) error) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	if _, err := c.GetNodeStatus(ctx); err != nil {
		return err
	}
	stream, err := c.sandboxRuntime.ReadFile(ctx, request)
	if err != nil {
		return err
	}
	for {
		frame, err := stream.Recv()
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return err
		}
		if err := consume(frame); err != nil {
			return err
		}
	}
}
func (c *Client) Mkdir(ctx context.Context, request *nodev1.RuntimeDirectoryRequest) (*guestv1.FileInfo, error) {
	if _, err := c.GetNodeStatus(ctx); err != nil {
		return nil, err
	}
	return c.sandboxRuntime.MakeDirectory(ctx, request)
}

var _ sandboxfiles.Backend = (*Client)(nil)
