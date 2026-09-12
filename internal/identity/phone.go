package identity

const defaultRegion = "GH"

func NormalizePhone(raw string) (string, error) {
	num, err := phonenumbers.Parse(raw, defaultRegion)
}
