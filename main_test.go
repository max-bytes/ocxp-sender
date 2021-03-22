package main

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestParse(t *testing.T) {
	timestamp := time.Date(2021, time.November, 1, 3, 0, 0, 0, time.UTC)
	b, err := parse("host", "service", 0, variableFlags{"a=xyz", "b=23", "c=asd"}, "/=2643MB;5948;5958;0;5968", timestamp)
	assert.Nil(t, err)

	expected := "metric,label=/,host=host,service=service,a=xyz,b=23,c=asd,uom=MB value=2643,warn=5948,crit=5958,min=0,max=5968 1635735600000000000\nstate,host=host,service=service,a=xyz,b=23,c=asd value=0i 1635735600000000000\n"
	assert.Equal(t, expected, b.String())
}

func BenchmarkParse(b *testing.B) {
	for i := 0; i < b.N; i++ {
		_, _ = parse("host", "service", 0, variableFlags{"a=xyz", "b=23", "c=asd"}, "/=2643MB;5948;5958;0;5968", time.Now())
	}
}
