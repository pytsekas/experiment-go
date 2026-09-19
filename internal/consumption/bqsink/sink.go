// Package bqsink stores readings in BigQuery through the Storage Write API.
package bqsink

import (
	"context"
	"errors"
	"fmt"

	"cloud.google.com/go/bigquery/storage/managedwriter"
	"cloud.google.com/go/bigquery/storage/managedwriter/adapt"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/types/dynamicpb"

	"github.com/pytsekas/experiment-go/internal/consumption"
)

// Config identifies the destination table.
type Config struct {
	Project string
	Dataset string
	Table   string
}

// Sink appends readings to one BigQuery table using the default stream, which
// is at-least-once: duplicates are resolved by the readings_current view
// rather than by the write path.
type Sink struct {
	client     *managedwriter.Client
	stream     *managedwriter.ManagedStream
	descriptor protoreflect.MessageDescriptor
}

var _ consumption.Sink = (*Sink)(nil)

// New opens a managed stream against cfg's table. The caller closes the sink.
func New(ctx context.Context, cfg Config) (*Sink, error) {
	client, err := managedwriter.NewClient(ctx, cfg.Project)
	if err != nil {
		return nil, fmt.Errorf("bqsink: new client: %w", err)
	}

	storageSchema, err := adapt.BQSchemaToStorageTableSchema(TableSchema())
	if err != nil {
		_ = client.Close()

		return nil, fmt.Errorf("bqsink: converting schema: %w", err)
	}

	messageDescriptor, err := adapt.StorageSchemaToProto2Descriptor(storageSchema, "readings")
	if err != nil {
		_ = client.Close()

		return nil, fmt.Errorf("bqsink: building descriptor: %w", err)
	}

	md, ok := messageDescriptor.(protoreflect.MessageDescriptor)
	if !ok {
		_ = client.Close()

		return nil, fmt.Errorf("bqsink: descriptor is %T, want a message descriptor", messageDescriptor)
	}

	descriptorProto, err := adapt.NormalizeDescriptor(md)
	if err != nil {
		_ = client.Close()

		return nil, fmt.Errorf("bqsink: normalising descriptor: %w", err)
	}

	stream, err := client.NewManagedStream(ctx,
		managedwriter.WithDestinationTable(
			managedwriter.TableParentFromParts(cfg.Project, cfg.Dataset, cfg.Table)),
		managedwriter.WithType(managedwriter.DefaultStream),
		managedwriter.WithSchemaDescriptor(descriptorProto))
	if err != nil {
		_ = client.Close()

		return nil, fmt.Errorf("bqsink: opening stream: %w", err)
	}

	return &Sink{client: client, stream: stream, descriptor: md}, nil
}

// Write appends one batch and waits for BigQuery to accept it, so the caller
// only acknowledges a message whose rows are durable.
func (s *Sink) Write(ctx context.Context, readings []consumption.Reading) error {
	if len(readings) == 0 {
		return nil
	}

	rows := make([][]byte, 0, len(readings))
	for _, r := range readings {
		message := dynamicpb.NewMessage(s.descriptor)
		for name, value := range readingToValues(r) {
			field := s.descriptor.Fields().ByTextName(name)
			if field == nil {
				return fmt.Errorf("bqsink: no field %q in the table descriptor", name)
			}
			message.Set(field, protoreflect.ValueOf(value))
		}

		encoded, err := proto.Marshal(message)
		if err != nil {
			return fmt.Errorf("bqsink: encoding row: %w", err)
		}
		rows = append(rows, encoded)
	}

	result, err := s.stream.AppendRows(ctx, rows)
	if err != nil {
		return classify(err)
	}
	if _, err = result.GetResult(ctx); err != nil {
		return classify(err)
	}

	return nil
}

// Close releases the stream and the client. Both are closed unconditionally
// so a failure closing the stream can never leak the client.
func (s *Sink) Close() error {
	streamErr := s.stream.Close()
	clientErr := s.client.Close()

	return joinCloseErrors(streamErr, clientErr)
}

// joinCloseErrors wraps each non-nil close error with its own context and
// joins them, so a caller can errors.Is either one out of the result. It
// returns nil when both closes succeeded.
func joinCloseErrors(streamErr, clientErr error) error {
	if streamErr != nil {
		streamErr = fmt.Errorf("bqsink: closing stream: %w", streamErr)
	}
	if clientErr != nil {
		clientErr = fmt.Errorf("bqsink: closing client: %w", clientErr)
	}

	return errors.Join(streamErr, clientErr)
}

// classify marks the failures worth retrying. Anything else is permanent: a
// schema mismatch or a missing permission will fail identically forever.
func classify(err error) error {
	if err == nil {
		return nil
	}

	switch status.Code(err) {
	case codes.Unavailable, codes.DeadlineExceeded, codes.ResourceExhausted, codes.Internal:
		return fmt.Errorf("%w: bigquery append: %w", consumption.ErrTransient, err)
	default:
		return fmt.Errorf("bigquery append: %w", err)
	}
}
