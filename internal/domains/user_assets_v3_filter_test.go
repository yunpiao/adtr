package domains

import (
	"context"
	"fmt"
	"testing"
)

func validUserAssetsV3Filter() UserAssetsV3Filter {
	return UserAssetsV3Filter{UserAssetsV2Filter: validUserAssetsV2Filter()}
}

func TestUserAssetsV3UACDisableBitPartitionsNullableUint32(t *testing.T) {
	cases := []struct {
		value *uint32
		want  string
	}{{nil, "not_returned"}}
	for _, value := range []uint32{0, 512, 16, 2147483648} {
		cases = append(cases, struct {
			value *uint32
			want  string
		}{&value, "clear"})
	}
	for _, value := range []uint32{2, 514, 18, 65538, 2147483650, 4294967295} {
		cases = append(cases, struct {
			value *uint32
			want  string
		}{&value, "set"})
	}
	for i, item := range cases {
		t.Run(fmt.Sprint(i), func(t *testing.T) {
			matches := 0
			for _, filter := range []string{"set", "clear", "not_returned"} {
				got := userAssetsV3UACDisableBitMatches(item.value, filter)
				if got != (filter == item.want) {
					t.Fatalf("filter %q: %v", filter, got)
				}
				if got {
					matches++
				}
			}
			if matches != 1 || !userAssetsV3UACDisableBitMatches(item.value, "") || userAssetsV3UACDisableBitMatches(item.value, "unknown") {
				t.Fatal("partitions overlap, omit does not include all, or unknown matches")
			}
		})
	}
}

func TestUserAssetsV3UACDisableBitValidationBeforeSQL(t *testing.T) {
	for _, value := range []string{"", "set", "clear", "not_returned"} {
		f := validUserAssetsV3Filter()
		f.UACDisableBit = value
		if err := ValidateUserAssetsV3Filter(f); err != nil {
			t.Fatal(value, err)
		}
	}
	for _, value := range []string{"all", "active", "disabled", "locked", "0", "1", "2", "SET", "Clear", " set", "set ", "not-returned", "null", "\x00", "\xff"} {
		f := validUserAssetsV3Filter()
		f.UACDisableBit = value
		requireUserAssetsV2Error(t, ValidateUserAssetsV3Filter(f), 400, "invalid_input")
		_, err := New(nil).UserAssetsV3ListTx(context.Background(), nil, "tenant", []string{"domain"}, f)
		requireUserAssetsV2Error(t, err, 400, "invalid_input")
	}
}
