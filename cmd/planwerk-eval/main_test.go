package main

import (
	"errors"
	"reflect"
	"testing"

	"github.com/planwerk/planwerk-agent/internal/eval"
	"github.com/planwerk/planwerk-agent/internal/report"
)

func TestRetry(t *testing.T) {
	t.Run("a failed attempt is retried and the retry's run returned", func(t *testing.T) {
		want := eval.Run{Usage: report.Usage{Calls: 7}}
		attempts := 0
		got, err := retry(func() (eval.Run, error) {
			attempts++
			if attempts == 1 {
				return eval.Run{}, errors.New("claude timed out")
			}
			return want, nil
		})
		if err != nil {
			t.Fatalf("retry = %v, want the second attempt's run", err)
		}
		if !reflect.DeepEqual(got, want) || attempts != 2 {
			t.Errorf("retry = %+v after %d attempts, want %+v after 2", got, attempts, want)
		}
	})

	t.Run("every attempt failing returns the last error", func(t *testing.T) {
		attempts := 0
		_, err := retry(func() (eval.Run, error) {
			attempts++
			return eval.Run{}, errors.New("claude timed out")
		})
		if err == nil || err.Error() != "claude timed out" {
			t.Errorf("retry = %v, want the attempt's error", err)
		}
		if attempts != maxRunAttempts {
			t.Errorf("attempts = %d, want %d", attempts, maxRunAttempts)
		}
	})
}
