package domains

// UserAssetsV3Filter adds a fact about the saved UAC value, not a computed
// account status. An omitted filter preserves the v2-compatible query behavior.
type UserAssetsV3Filter struct {
	UserAssetsV2Filter
	UACDisableBit string
}

func ValidateUserAssetsV3Filter(f UserAssetsV3Filter) error {
	if err := ValidateUserAssetsV2Filter(f.UserAssetsV2Filter); err != nil {
		return err
	}
	switch f.UACDisableBit {
	case "", "set", "clear", "not_returned":
		return nil
	default:
		return problem(400, "invalid_input")
	}
}

func userAssetsV3UACDisableBitMatches(value *uint32, filter string) bool {
	switch filter {
	case "":
		return true
	case "not_returned":
		return value == nil
	case "set":
		return value != nil && *value&0x00000002 != 0
	case "clear":
		return value != nil && *value&0x00000002 == 0
	default:
		return false
	}
}
