package agent

import (
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func isFailedPrecondition(err error) bool {
	return status.Code(err) == codes.FailedPrecondition
}
