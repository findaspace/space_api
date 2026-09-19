package identity

import (
	"fmt"

	"github.com/nyaruka/phonenumbers/v2"
)

const defaultRegion = "GH"

func NormalizePhone(raw string) (string, error) {
	num, err := phonenumbers.Parse(raw, defaultRegion)
	if err != nil {
		return "", fmt.Errorf("%w: %s", ErrInvalidPhone, err)
	}
	if !phonenumbers.IsValidNumber(num) {
		return "", ErrInvalidPhone
	}

	switch phonenumbers.GetNumberType(num) {
	case phonenumbers.MOBILE, phonenumbers.FIXED_LINE_OR_MOBILE:
	default:
		return "", fmt.Errorf("%w: not a mobile number", ErrInvalidPhone)
	}

	return phonenumbers.Format(num, phonenumbers.E164), nil
}

func MaskPhone(e164 string) string {
	if len(e164) < 7 {
		return "***"
	}
	return e164[:len(e164)-6] + "******"
}
