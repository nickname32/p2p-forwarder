package main

import (
	"bufio"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"
	"unicode"

	p2pforwarder "github.com/nickname32/p2p-forwarder"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
)

func init() {
	logger, err := zap.Config{
		Level:       zap.NewAtomicLevelAt(zap.InfoLevel),
		Development: false,
		Encoding:    "console",
		EncoderConfig: zapcore.EncoderConfig{
			TimeKey:     "Time",
			LevelKey:    "Level",
			MessageKey:  "Message",
			LineEnding:  zapcore.DefaultLineEnding,
			EncodeLevel: zapcore.LowercaseLevelEncoder,
			EncodeTime: func(t time.Time, enc zapcore.PrimitiveArrayEncoder) {
				enc.AppendString(t.Format("2006/01/02 15:04:05.000"))
			},
			EncodeDuration: nil,
			EncodeCaller:   nil,
		},
		OutputPaths:      []string{"stdout"},
		ErrorOutputPaths: []string{"stderr"},
	}.Build()
	if err != nil {
		panic(err)
	}

	zap.ReplaceGlobals(logger)

	p2pforwarder.OnError(func(err error) {
		zap.S().Error(err)
	})
	p2pforwarder.OnInfo(func(str string) {
		zap.L().Info(str)
	})
}

type strArrFlags []string

func (saf *strArrFlags) String() string {
	return fmt.Sprint([]string(*saf))
}

func (saf *strArrFlags) Set(value string) error {
	*saf = append(*saf, value)
	return nil
}

type uint16ArrFlags []uint16

func (uaf *uint16ArrFlags) String() string {
	return fmt.Sprint([]uint16(*uaf))
}

func (uaf *uint16ArrFlags) Set(value string) error {
	n, err := strconv.ParseUint(value, 10, 16)
	if err != nil {
		return err
	}

	*uaf = append(*uaf, uint16(n))
	return nil
}

var (
	fwr          *p2pforwarder.Forwarder
	connections  = make(map[string]func())
	openTCPPorts = make(map[uint16]func())
	openUDPPorts = make(map[uint16]func())
)

func main() {
	connectIds := strArrFlags{}
	flag.Var(&connectIds, "connect", "Add id you want connect to (can be used multiple times).")

	tcpPorts := uint16ArrFlags{}
	flag.Var(&tcpPorts, "tcp", "Add tcp port you want to open (can be used multiple times).")

	udpPorts := uint16ArrFlags{}
	flag.Var(&udpPorts, "udp", "Add udp port you want to open (can be used multiple times).")

	flag.Parse()

	zap.L().Info("Initialization...")

	var (
		cancel func()
		err    error
	)
	fwr, cancel, err = p2pforwarder.NewForwarder()
	if err != nil {
		zap.S().Error(err)
	}

	zap.L().Info("Your id: " + fwr.ID())

	for _, port := range tcpPorts {
		zap.L().Info("Opening tcp:" + strconv.FormatUint(uint64(port), 10))

		cancel, err := fwr.OpenPort("tcp", port)
		if err != nil {
			zap.S().Error(err)
			continue
		}

		openTCPPorts[port] = cancel
	}
	for _, port := range udpPorts {
		zap.L().Info("Opening udp: " + strconv.FormatUint(uint64(port), 10))

		cancel, err := fwr.OpenPort("udp", port)
		if err != nil {
			zap.S().Error(err)
			continue
		}

		openUDPPorts[port] = cancel
	}

	for _, id := range connectIds {
		zap.L().Info("Connecting to " + id)

		listenip, cancel, err := fwr.Connect(id)
		if err != nil {
			zap.S().Error(err)
			continue
		}

		connections[id] = cancel

		zap.L().Info("Connections to " + id + "'s ports are listened on " + listenip)
	}

	zap.L().Info("Initialization completed")

	cmdch := make(chan string)

	go func() {
		scanner := bufio.NewScanner(os.Stdin)

		for {
			scanner.Scan()
			err = scanner.Err()

			if err != nil {
				zap.S().Error(err)
				continue
			}

			cmdch <- scanner.Text()
		}
	}()

	termSignal := make(chan os.Signal, 1)
	signal.Notify(termSignal, syscall.SIGINT, syscall.SIGTERM, os.Interrupt, os.Kill)

	executeCommand("")

loop:
	for {
		select {
		case str := <-cmdch:
			executeCommand(str)
		case <-termSignal:
			zap.L().Info("Shutdown...")

			cancel()

			for _, cancel := range connections {
				cancel()
			}
			for _, cancel := range openTCPPorts {
				cancel()
			}
			for _, cancel := range openUDPPorts {
				cancel()
			}

			break loop
		}
	}
}

func parseArgs(argsStr string, n int) []string {
	if n <= 0 {
		return []string{}
	}

	args := make([]string, n)

	argsRunes := []rune(argsStr)

	argStart := -1

	a := 0

	for i := 0; i < len(argsRunes); i++ {
		if argStart == -1 {
			if unicode.IsSpace(argsRunes[i]) {
				continue
			} else {
				argStart = i
			}
		} else {
			if a+1 == n {
				args[a] = string(argsRunes[argStart:])
				break
			} else {
				if unicode.IsSpace(argsRunes[i]) {
					args[a] = string(argsRunes[argStart:i])
					argStart = -1
					a++
				}
			}
		}
	}

	if argStart != -1 {
		args[a] = string(argsRunes[argStart:])
		argStart = -1
		a++
	}

	return args
}

func executeCommand(str string) {
	args := parseArgs(str, 3)
	cmd := strings.ToLower(args[0])
	params := args[1:]

	switch cmd {
	case "connect":
		id := params[0]

		zap.L().Info("Connecting to " + id)

		listenip, cancel, err := fwr.Connect(id)
		if err != nil {
			zap.S().Error(err)
			return
		}

		connections[id] = cancel

		zap.L().Info("Connections to " + id + "'s ports are listened on " + listenip)

	case "disconnect":
		id := params[0]

		close := connections[id]

		if close == nil {
			zap.L().Error("You are not connected to specified id")
			return
		}

		zap.L().Info("Disconnecting from " + id)

		close()

		delete(connections, id)

	case "open":
		networkType := strings.ToLower(params[0])

		portStr := params[1]
		portUint64, err := strconv.ParseUint(portStr, 10, 16)
		if err != nil {
			zap.S().Error(err)
			return
		}
		port := uint16(portUint64)

		zap.L().Info("Opening " + networkType + ":" + portStr)

		cancel, err := fwr.OpenPort(networkType, port)
		if err != nil {
			zap.S().Error(err)
			return
		}

		switch networkType {
		case "tcp":
			openTCPPorts[port] = cancel
		case "udp":
			openUDPPorts[port] = cancel
		}
	case "close":
		networkType := strings.ToLower(params[0])
		portStr := params[1]
		portUint64, err := strconv.ParseUint(portStr, 10, 16)
		if err != nil {
			zap.S().Error(err)
			return
		}
		port := uint16(portUint64)

		var cancel func()
		switch networkType {
		case "tcp":
			cancel = openTCPPorts[port]
		case "udp":
			cancel = openUDPPorts[port]
		}

		if cancel == nil {
			zap.L().Error("Specified port is not opened")
			return
		}

		zap.L().Info("Closing " + networkType + ":" + portStr)

		cancel()

		delete(openTCPPorts, port)
	default:
		zap.L().Info("")
		zap.L().Info("Cli commands list:")
		zap.L().Info("connect [ID_HERE]")
		zap.L().Info("disconnect [ID_HERE]")
		zap.L().Info("open [TCP_OR_UDP_HERE] [PORT_NUMBER_HERE]")
		zap.L().Info("close [UDP_OR_UDP_HERE] [PORT_NUMBER_HERE]")
		zap.L().Info("")
	}
}
