package cli

func shortID(id string) string {
	if len(id) <= ShortIDLength {
		return id
	}
	return id[:ShortIDLength]
}
