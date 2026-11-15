//go:build !windows

package systeminit

import (
	"log"
	"math"
	"reflect"
	"strconv"
	"strings"
	"syscall"
)

const (
	desiredOpenFileLimit      uint64 = 1048576
	desiredThreadProcessLimit uint64 = 1048576
)

func colorsEnabled() bool {
	return true
}

func raiseResourceLimit(resource int, desiredLimit uint64) (uint64, uint64, uint64, string, bool) {
	var currentLimit syscall.Rlimit
	if err := syscall.Getrlimit(resource, &currentLimit); err != nil {
		return 0, desiredLimit, 0, stateUnsupported, true
	}
	currentValue := rlimitFieldUint(reflect.ValueOf(currentLimit).FieldByName("Cur"))
	maxValue := rlimitFieldUint(reflect.ValueOf(currentLimit).FieldByName("Max"))
	if currentValue >= desiredLimit {
		return currentValue, desiredLimit, currentValue, stateSkipped, false
	}
	nextLimit := currentLimit
	if maxValue < desiredLimit {
		setRlimitField(reflect.ValueOf(&nextLimit).Elem().FieldByName("Max"), desiredLimit)
	}
	setRlimitField(reflect.ValueOf(&nextLimit).Elem().FieldByName("Cur"), desiredLimit)
	if err := syscall.Setrlimit(resource, &nextLimit); err != nil {
		log.Printf("SYSTEM LIMIT resource=%d requested_soft=%d requested_hard=%d rejected=%v", resource, desiredLimit, desiredLimit, err)
		if maxValue <= currentValue {
			return currentValue, desiredLimit, currentValue, stateSkipped, true
		}
		fallback := currentLimit
		setRlimitField(reflect.ValueOf(&fallback).Elem().FieldByName("Cur"), maxValue)
		if fallbackErr := syscall.Setrlimit(resource, &fallback); fallbackErr != nil {
			log.Printf("SYSTEM LIMIT resource=%d fallback_soft=%d rejected=%v", resource, maxValue, fallbackErr)
			return currentValue, desiredLimit, currentValue, stateSkipped, true
		}
	}
	if err := syscall.Getrlimit(resource, &currentLimit); err != nil {
		return currentValue, desiredLimit, currentValue, stateSkipped, true
	}
	afterValue := rlimitFieldUint(reflect.ValueOf(currentLimit).FieldByName("Cur"))
	afterMax := rlimitFieldUint(reflect.ValueOf(currentLimit).FieldByName("Max"))
	log.Printf("SYSTEM LIMIT resource=%d soft_before=%d hard_before=%d soft_after=%d hard_after=%d requested=%d", resource, currentValue, maxValue, afterValue, afterMax, desiredLimit)
	status := stateSkipped
	if afterValue > currentValue {
		status = stateApplied
	}
	return currentValue, desiredLimit, afterValue, status, afterValue < desiredLimit
}

func rlimitFieldUint(field reflect.Value) uint64 {
	if !field.IsValid() {
		return 0
	}
	switch field.Kind() {
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		value := field.Int()
		if value < 0 {
			return math.MaxUint64
		}
		return uint64(value)
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr:
		return field.Uint()
	default:
		return 0
	}
}

func setRlimitField(field reflect.Value, value uint64) {
	if !field.IsValid() || !field.CanSet() {
		return
	}
	switch field.Kind() {
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		if value > math.MaxInt64 {
			value = math.MaxInt64
		}
		field.SetInt(int64(value))
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr:
		field.SetUint(value)
	}
}

func formatLimit(limit uint64) string {
	if limit == 0 {
		return stateUnsupported
	}
	return strconv.FormatUint(limit, 10)
}

func resourceLimitSetting(name string, before uint64, target uint64, after uint64, status string, notes string) tuningSetting {
	if status == stateUnsupported {
		return tuningSetting{Name: name, Before: "-", Target: formatLimit(target), After: "-", Status: status, Notes: notes}
	}
	return tuningSetting{
		Name:   name,
		Before: formatLimit(before),
		Target: formatLimit(target),
		After:  formatLimit(after),
		Status: status,
		Notes:  notes,
	}
}

func unixSocketControl(network string, address string, connection syscall.RawConn, reusePortSupported bool) error {
	if !strings.HasPrefix(strings.ToLower(strings.TrimSpace(network)), "tcp") {
		return nil
	}
	_ = address
	controlErr := connection.Control(func(fileDescriptor uintptr) {
		fd := int(fileDescriptor)
		_ = syscall.SetsockoptInt(fd, syscall.SOL_SOCKET, syscall.SO_REUSEADDR, 1)
		if reusePortSupported {
			if reusePortOption, found := platformReusePortSocketOption(); found {
				_ = syscall.SetsockoptInt(fd, syscall.SOL_SOCKET, reusePortOption, 1)
			}
		}
		_ = syscall.SetsockoptInt(fd, syscall.SOL_SOCKET, syscall.SO_KEEPALIVE, 1)
		_ = syscall.SetsockoptInt(fd, syscall.IPPROTO_TCP, syscall.TCP_NODELAY, 1)
	})
	if controlErr != nil {
		return nil
	}
	return nil
}
