package forge

import (
	"errors"
	"strings"
)

func joinComma(items []string) string { return strings.Join(items, ",") }

func redactToken(err error, secret string) error {
	if secret == "" || err == nil {
		return err
	}
	return errors.New(strings.ReplaceAll(err.Error(), secret, "***"))
}
