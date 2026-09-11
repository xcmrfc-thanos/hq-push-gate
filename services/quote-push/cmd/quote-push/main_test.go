package main

import (
	"testing"

	"github.com/segmentio/kafka-go"
)

func TestOffsetPendingKeepsMaxOffset(t *testing.T) {
	p := &offsetPending{}
	for _, off := range []int64{10, 3, 7, 20} {
		p.Mark(kafka.Message{Offset: off})
	}
	m := p.Take()
	if m == nil || m.Offset != 20 {
		t.Fatalf("expect max offset 20, got %+v", m)
	}
	if p.Take() != nil {
		t.Fatal("expect empty after Take")
	}
}

func TestOffsetPendingEmptyTake(t *testing.T) {
	p := &offsetPending{}
	if p.Take() != nil {
		t.Fatal("expect nil on empty pending")
	}
}
