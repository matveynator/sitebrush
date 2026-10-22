// Package buildconsole presents build plans without hiding compiler output.
package buildconsole

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"golang.org/x/term"
)

type Console struct {
	Output io.Writer
	color  bool
	live   bool
}

func IsTerminal(stream any) bool {
	file, ok := stream.(*os.File)
	return ok && term.IsTerminal(int(file.Fd()))
}

func validPolicy(policy string) bool {
	return policy == "auto" || policy == "always" || policy == "never"
}

func Interactive(policy string, hasOptions bool, input io.Reader, output io.Writer) (bool, error) {
	if !validPolicy(policy) {
		return false, errors.New("-interactive must be auto, always, or never")
	}
	return policy == "always" || policy == "auto" && !hasOptions && IsTerminal(input) && IsTerminal(output), nil
}

func New(output io.Writer, policy string) (*Console, error) {
	if !validPolicy(policy) {
		return nil, errors.New("-color must be auto, always, or never")
	}
	_, noColor := os.LookupEnv("NO_COLOR")
	live := IsTerminal(output)
	return &Console{Output: output, live: live, color: policy == "always" || policy == "auto" && live && !noColor && os.Getenv("TERM") != "dumb"}, nil
}

func (console *Console) styled(code, message string) string {
	if !console.color {
		return message
	}
	return "\x1b[" + code + "m" + message + "\x1b[0m"
}

func (console *Console) Heading(message string) {
	fmt.Fprintf(console.Output, "\n%s\n", console.styled("1;36", message))
}

func (console *Console) Bold(message string) string {
	return console.styled("1", message)
}

func (console *Console) Info(format string, arguments ...any) {
	fmt.Fprintf(console.Output, format+"\n", arguments...)
}

func (console *Console) OK(message string) {
	fmt.Fprintln(console.Output, console.styled("32", "[OK] "+message))
}

func (console *Console) Warning(message string) {
	fmt.Fprintln(console.Output, console.styled("33", "[!] "+message))
}

func (console *Console) Error(message string) {
	fmt.Fprintln(console.Output, console.styled("31", "[FAILED] "+message))
}

// The caller owns the reply channel; the operation never mutates presentation
// state. Periodic complete lines keep streamed subprocess logs readable.
func (console *Console) Step(number, total int, title string, operation func() error) error {
	console.Heading(fmt.Sprintf("[%d/%d] %s", number, total, title))
	started := time.Now()
	finished := make(chan error, 1)
	go func() { finished <- operation() }()
	ticker := time.NewTicker(10 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case err := <-finished:
			duration := time.Since(started).Round(time.Second)
			if err != nil {
				console.Error(fmt.Sprintf("%s (%s): %v", title, duration, err))
				return fmt.Errorf("%s: %w", title, err)
			}
			console.OK(fmt.Sprintf("%s (%s)", title, duration))
			return nil
		case <-ticker.C:
			if console.live {
				console.Info("  %s — still working, elapsed %s", title, time.Since(started).Round(time.Second))
			}
		}
	}
}

func (console *Console) Line(reader *bufio.Reader, label, fallback string) (string, error) {
	if fallback == "" {
		fmt.Fprintf(console.Output, "%s: ", label)
	} else {
		fmt.Fprintf(console.Output, "%s [%s]: ", label, console.Bold(fallback))
	}
	answer, err := reader.ReadString('\n')
	if err != nil {
		return "", fmt.Errorf("input ended before confirmation: %w", err)
	}
	answer = strings.TrimSpace(answer)
	if answer == "" {
		return fallback, nil
	}
	return answer, nil
}

func (console *Console) Confirm(reader *bufio.Reader, label string, fallback bool) (bool, error) {
	defaultAnswer := "no"
	if fallback {
		defaultAnswer = "yes"
	}
	for {
		answer, err := console.Line(reader, label, defaultAnswer)
		if err != nil {
			return false, err
		}
		switch strings.ToLower(answer) {
		case "y", "yes":
			return true, nil
		case "n", "no":
			return false, nil
		default:
			console.Warning("Please enter yes or no.")
		}
	}
}
