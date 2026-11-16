package env

import "testing"

func TestOr(t *testing.T) {
	t.Setenv("AULAGLOBAL_TEST_VALUE", "set")
	if got := Or("AULAGLOBAL_TEST_VALUE", "fallback"); got != "set" {
		t.Errorf("set variable = %q", got)
	}
	t.Setenv("AULAGLOBAL_TEST_VALUE", "")
	if got := Or("AULAGLOBAL_TEST_VALUE", "fallback"); got != "fallback" {
		t.Errorf("empty variable = %q", got)
	}
	if got := Or("AULAGLOBAL_TEST_UNSET_VARIABLE", "fallback"); got != "fallback" {
		t.Errorf("unset variable = %q", got)
	}
}
