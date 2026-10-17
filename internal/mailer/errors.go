package mailer

import (
	"errors"
	"net/textproto"
	"strconv"
	"strings"
)

func smtpCode(err error) int {
	var te *textproto.Error
	if errors.As(err, &te) {
		return te.Code
	}
	return 0
}

// scrubCode keeps only the numeric reply code and a generic word: server messages often echo the
// recipient address, which must not reach logs or the database.
func scrubCode(err error) string {
	if c := smtpCode(err); c != 0 {
		return strings.TrimSpace("server replied " + strconv.Itoa(c))
	}
	return "failed"
}
