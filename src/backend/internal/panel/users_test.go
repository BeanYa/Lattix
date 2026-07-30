package panel

import "testing"

func TestValidateTrafficResetDay(t *testing.T) {
	for _, day := range []int{0, 1, 28, 29, 30, 31} {
		if err := validateTrafficResetDay(day); err != nil {
			t.Errorf("day %d rejected: %v", day, err)
		}
	}
	for _, day := range []int{-1, 32} {
		if err := validateTrafficResetDay(day); err == nil {
			t.Errorf("day %d accepted", day)
		}
	}
}
