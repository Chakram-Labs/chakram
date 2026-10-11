package consensus

import (
	"testing"
)

func TestBlockRewardWeiSchedule(t *testing.T) {
	cases := []struct {
		height uint64
		want   uint64
	}{
		{0, BaseRewardWei},                               // genesis: 5 CHK
		{1, BaseRewardWei},                               // first mined block
		{HalvingInterval - 1, BaseRewardWei},             // last block of era 0
		{HalvingInterval, BaseRewardWei >> 1},            // first block of era 1
		{HalvingInterval * 2, BaseRewardWei >> 2},        // era 2
		{HalvingInterval * 17, TailEmissionWei},          // floor (0.3 CHK)
		{HalvingInterval * 100, TailEmissionWei},         // deep future
	}
	for _, c := range cases {
		if got := BlockRewardWei(c.height); got != c.want {
			t.Errorf("BlockRewardWei(%d): got %d, want %d", c.height, got, c.want)
		}
	}
}

func TestBlockRewardGweiConversion(t *testing.T) {
	// Base reward in Gwei: 5e18 / 1e9 = 5e9 Gwei.
	if got, want := BlockRewardGwei(0), uint64(5_000_000_000); got != want {
		t.Errorf("BlockRewardGwei(0): got %d, want %d", got, want)
	}
	// Tail emission in Gwei: 3e17 / 1e9 = 3e8 Gwei.
	if got, want := BlockRewardGwei(HalvingInterval*100), uint64(300_000_000); got != want {
		t.Errorf("BlockRewardGwei(tail): got %d, want %d", got, want)
	}
}

func TestWithdrawalIndex(t *testing.T) {
	if got := WithdrawalIndex(1); got != 0 {
		t.Errorf("WithdrawalIndex(1): got %d, want 0", got)
	}
	if got := WithdrawalIndex(100); got != 99 {
		t.Errorf("WithdrawalIndex(100): got %d, want 99", got)
	}
}

func TestWithdrawalIndexGenesisIsPanic(t *testing.T) {
	defer func() {
		if r := recover(); r == nil {
			t.Error("expected panic for genesis height, got none")
		}
	}()
	WithdrawalIndex(0)
}

func TestQuantityHex(t *testing.T) {
	cases := []struct {
		v    uint64
		want string
	}{
		{0, "0x0"},
		{1, "0x1"},
		{255, "0xff"},
		{256, "0x100"},
		{0xdeadbeef, "0xdeadbeef"},
		{^uint64(0), "0xffffffffffffffff"},
	}
	for _, c := range cases {
		if got := QuantityHex(c.v); got != c.want {
			t.Errorf("QuantityHex(%d): got %q, want %q", c.v, got, c.want)
		}
	}
}

func TestParseQuantityRoundTrip(t *testing.T) {
	values := []uint64{0, 1, 255, 256, 0xdeadbeef, ^uint64(0)}
	for _, v := range values {
		s := QuantityHex(v)
		got, err := ParseQuantity(s)
		if err != nil {
			t.Errorf("ParseQuantity(%q): unexpected error: %v", s, err)
			continue
		}
		if got != v {
			t.Errorf("round-trip %d: got %d via %q", v, got, s)
		}
	}
}

func TestParseQuantityBadInput(t *testing.T) {
	bads := []string{"", "0xgg", "0xffffffffffffffff1"}
	for _, s := range bads {
		if _, err := ParseQuantity(s); err == nil && s != "" {
			t.Errorf("ParseQuantity(%q): expected error, got nil", s)
		}
	}
}

func TestAddChecked(t *testing.T) {
	v, overflow := AddChecked(1, 2)
	if overflow || v != 3 {
		t.Errorf("AddChecked(1,2): got (%d, %v)", v, overflow)
	}
	_, overflow = AddChecked(^uint64(0), 1)
	if !overflow {
		t.Error("AddChecked(max, 1): expected overflow")
	}
}
