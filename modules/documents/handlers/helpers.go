package handlers

import (
	"io"

	"github.com/google/uuid"

	"github.com/mohamadhallal/zentax-api/delivery/httpkit/httperr"
)

func closeBody(u *upload) {
	if c, ok := u.File.Body.(io.Closer); ok {
		_ = c.Close()
	}
}

// nonEmpty turns an empty form field into "absent".
func nonEmpty(v *string) *string {
	if v == nil || *v == "" {
		return nil
	}
	return v
}

func isUUID(s string) bool {
	_, err := uuid.Parse(s)
	return err == nil
}

func validation(msg string) error {
	return httperr.New(httperr.ErrValidation, "INVALID_BODY", msg)
}
