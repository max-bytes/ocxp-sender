package main

import (
	"bytes"
	"fmt"
	"io"
	"log"
	"net"
	"os"
	"os/exec"
	"os/signal"
	"regexp"
	"runtime"
	"runtime/pprof"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	protocol "github.com/influxdata/line-protocol"
	flag "github.com/spf13/pflag"
	"github.com/streadway/amqp"
)

const ExchangeName = "naemon"
const DaemonAddress = "127.0.0.1:55550"

func main() {
	var host string
	var service string
	var output string
	var state int
	var variableFlags variableFlags
	var perfData string
	var daemonize bool
	var amqpURL string
	var cpuprofile string
	var memprofile string
	flag.VarP(&variableFlags, "var", "v", "variables in the form \"name=value\" (multiple -v allowed); get forwarded as tags")
	flag.StringVarP(&host, "host", "h", "", "Name of host")
	flag.StringVarP(&service, "service", "s", "", "Name of service")
	flag.IntVarP(&state, "state", "t", 0, "State of the check")
	flag.StringVarP(&output, "output", "o", "", "Output of the check result (optional)")
	flag.StringVarP(&perfData, "perfdata", "p", "", "Performance data")
	flag.StringVarP(&amqpURL, "amqp-url", "u", "amqp://localhost:5672", "URL of the AMQP (e.g. RabbitMQ) server to send the data to")
	flag.BoolVarP(&daemonize, "daemonize", "d", false, "Whether or not to spawn a daemon process that runs infinitely")
	flag.StringVarP(&cpuprofile, "cpuprofile", "", "", "write cpu profile to `file`")
	flag.StringVarP(&memprofile, "memprofile", "", "", "write memory profile to `file`")
	flag.Parse()

	if daemonize { // run as daemon
		if cpuprofile != "" {
			f, err := os.Create(cpuprofile)
			if err != nil {
				log.Fatal("could not create CPU profile: ", err)
			}
			defer f.Close() // error handling omitted for example
			if err := pprof.StartCPUProfile(f); err != nil {
				log.Fatal("could not start CPU profile: ", err)
			}
			defer pprof.StopCPUProfile()
		}

		fmt.Println("Running daemon...")
		runDaemon(DaemonAddress, amqpURL, 6*time.Minute)
		fmt.Println("Stopping daemon")

		if memprofile != "" {
			f, err := os.Create(memprofile)
			if err != nil {
				log.Fatal("could not create memory profile: ", err)
			}
			defer f.Close() // error handling omitted for example
			runtime.GC()    // get up-to-date statistics
			if err := pprof.WriteHeapProfile(f); err != nil {
				log.Fatal("could not write memory profile: ", err)
			}
		}

	} else { // run as regular program that sends its metrics to the daemon
		if !isFlagPassed("host") {
			fail("host name not set")
		}
		if !isFlagPassed("service") {
			fail("service name not set")
		}
		if !isFlagPassed("state") {
			fail("state not set")
		}
		if !isFlagPassed("perfdata") {
			fail("Performance data not set")
		}

		b, err := parse(host, service, state, output, variableFlags, perfData, time.Now())
		failOnError(err, "Failed to parse inputs")

		// only publish if there are actually metrics/perfdata
		if b.Len() > 0 {
			// fmt.Println("Sending:")
			//fmt.Println(b.String())

			conn, err := net.Dial("tcp", DaemonAddress)
			if err == nil { // try to connect to already running daemon
				defer conn.Close()
				// write to socket
				_, err = conn.Write(b.Bytes())
				failOnError(err, "Failed to write")
			} else {
				fmt.Println("Trying to spawn daemon")

				// spawn daemon
				binary, err := exec.LookPath(os.Args[0])
				failOnError(err, "Failed to lookup binary")
				_, _ = os.StartProcess(binary, []string{binary, "-d", "-u", amqpURL}, &os.ProcAttr{Dir: "", Env: nil,
					Files: []*os.File{nil, nil, nil}, Sys: nil})

				// we don't fail when daemon start failed, because maybe another run has started it successfully
				// failOnError(err, "Failed to spawn daemon")

				// give daemon time to startup
				time.Sleep(500 * time.Millisecond)

				// try to connect and send one more time
				conn, err := net.Dial("tcp", DaemonAddress)
				failOnError(err, "Failed to connect to daemon")
				defer conn.Close()
				_, err = conn.Write(b.Bytes())
				failOnError(err, "Failed to write")
			}
		}
	}
}

func runDaemon(listenAddress string, amqpURL string, inactivityTimeout time.Duration) {

	// setup TCP server
	connection, err := net.Listen("tcp", listenAddress)
	failOnError(err, "Failed to listen on port")
	defer connection.Close()

	// setup amqp connection
	amqpConnection, err := amqp.Dial(amqpURL)
	failOnError(err, "Failed to connect to RabbitMQ")
	defer amqpConnection.Close()
	channel, err := amqpConnection.Channel()
	failOnError(err, "Failed to open a channel")
	defer channel.Close()
	err = channel.ExchangeDeclare(
		ExchangeName, // name
		"fanout",     // type
		true,         // durable
		false,        // auto-deleted
		false,        // internal
		false,        // no-wait
		nil,          // arguments
	)
	failOnError(err, "Failed to declare an exchange")

	errorChan := make(chan error, 1)
	heartbeatChan := make(chan bool, 1)

	// signal handling to allow graceful exit
	stopSignal := make(chan os.Signal, 1)
	signal.Notify(stopSignal, os.Interrupt, syscall.SIGTERM)

	go func() {
		for {
			conn, err := connection.Accept()
			if err != nil {
				errorChan <- err
				return
			}

			go handleClient(conn, channel, errorChan, heartbeatChan)
		}
	}()

	inactivityTimer := time.NewTimer(inactivityTimeout)
L:
	for {
		inactivityTimer.Reset(inactivityTimeout)
		select {
		case err = <-errorChan:
			fmt.Printf("Encountered error while processing message: %v", err)
			break L
		case <-heartbeatChan: // heartbeat encountered, continue loop and restart select
		case <-inactivityTimer.C:
			fmt.Println("Reached inactivity timeout, closing...")
			break L
		case <-stopSignal:
			fmt.Println("Received stop signal, closing...")
			break L
		}

	}
}

const maxBufferSize = 163840

type Buffer struct {
	B []byte
}

var bufPool = sync.Pool{
	New: func() interface{} {
		b := new(Buffer)
		b.B = make([]byte, maxBufferSize)
		return b
	},
}

func handleClient(conn net.Conn, channel *amqp.Channel, doneChan chan error, heartbeatChan chan bool) {
	defer conn.Close()

	buffer := bufPool.Get().(*Buffer)
	tmp := bufPool.Get().(*Buffer)
	n := 0
	for {
		tmpN, err := conn.Read(tmp.B)
		if err != nil {
			if err != io.EOF {
				doneChan <- err
				return
			}
			break
		}
		buffer.B = append(buffer.B, tmp.B[:tmpN]...)
		n += tmpN
	}
	bufPool.Put(tmp)

	received := buffer.B[:n]

	// NOTE: we assume that amqp.Channel and its publish method are thread safe and one channel can be used in multiple goroutines
	// the documentation is not 100% clear on this, but there seems to be a proper lock/mutex in place:
	// https://github.com/streadway/amqp/blob/master/channel.go#L1331
	err := channel.Publish(
		ExchangeName, // exchange
		"",           // routing key
		false,        // mandatory
		false,        // immediate
		amqp.Publishing{
			ContentType:  "text/plain",
			Body:         received,
			DeliveryMode: 2,
		})
	if err != nil {
		doneChan <- err
		return
	}

	bufPool.Put(buffer)

	heartbeatChan <- true
}

func parse(host string, service string, state int, output string, variableFlags variableFlags, perfData string, timestamp time.Time) (*bytes.Buffer, error) {
	// create tags from variables
	tags := make([]*protocol.Tag, 0, len(variableFlags)+2)
	tags = append(tags, &(protocol.Tag{Key: "host", Value: host}))
	tags = append(tags, &(protocol.Tag{Key: "service", Value: service}))
	for _, item := range variableFlags {
		x := strings.SplitN(item, "=", 2)
		if len(x) < 2 {
			return nil, fmt.Errorf("variable %v could not be parsed into name=value", item)
		}
		tags = append(tags, &(protocol.Tag{Key: x[0], Value: x[1]}))
	}

	var b bytes.Buffer
	encoder := protocol.NewEncoder(&b)

	err := encodePerfData(perfData, tags, timestamp, encoder)
	if err != nil {
		return nil, err
	}

	// add state as its own metric, with the state encoded as an integer (0 to 3)
	stateMetric := state2metric("state", state, output, tags, timestamp)
	_, err = encoder.Encode(stateMetric)
	if err != nil {
		return nil, err
	}

	return &b, nil
}

func state2metric(metricName string, state int, output string, addedTags []*protocol.Tag, timestamp time.Time) Metric {
	var fields = []*protocol.Field{
		&(protocol.Field{Key: "value", Value: state}),
	}

	if len(output) > 0 {
		fields = append(fields, &(protocol.Field{Key: "output", Value: output}))
	}

	metric := Metric{
		name:      metricName,
		fields:    fields,
		tags:      addedTags,
		timestamp: timestamp,
	}

	return metric
}

// partly taken from https://github.com/Griesbacher/nagflux/blob/ea877539bc49ed67e9a5e35b8a127b1ff4cadaad/collector/spoolfile/nagiosSpoolfileWorker.go
var regexPerformanceLabel = regexp.MustCompile(`([^=]+)=(U|[\d\.,\-]+)([\pL\/%]*);?([\d\.,\-:~@]+)?;?([\d\.,\-:~@]+)?;?([\d\.,\-]+)?;?([\d\.,\-]+)?;?\s*`)

func encodePerfData(str string, addedTags []*protocol.Tag, timestamp time.Time, encoder *protocol.Encoder) error {
	perfSlices := regexPerformanceLabel.FindAllStringSubmatch(str, -1)

	for _, perfSlice := range perfSlices {
		label := perfSlice[1]

		v, err := strconv.ParseFloat(perfSlice[2], 64)
		if err != nil {
			fmt.Println(err)
			continue
		}

		var fields = []*protocol.Field{
			&(protocol.Field{Key: "value", Value: v}),
		}
		var tags = []*protocol.Tag{
			&(protocol.Tag{Key: "label", Value: label}),
		}
		tags = append(tags, addedTags...)

		// add UOM to tags, if present
		if perfSlice[3] != "" {
			tags = append(tags, &(protocol.Tag{Key: "uom", Value: perfSlice[3]}))
		}
		warnF, err := strconv.ParseFloat(perfSlice[4], 64)
		if err == nil {
			fields = append(fields, &(protocol.Field{Key: "warn", Value: warnF}))
		}
		critF, err := strconv.ParseFloat(perfSlice[5], 64)
		if err == nil {
			fields = append(fields, &(protocol.Field{Key: "crit", Value: critF}))
		}
		minF, err := strconv.ParseFloat(perfSlice[6], 64)
		if err == nil {
			fields = append(fields, &(protocol.Field{Key: "min", Value: minF}))
		}
		maxF, err := strconv.ParseFloat(perfSlice[7], 64)
		if err == nil {
			fields = append(fields, &(protocol.Field{Key: "max", Value: maxF}))
		}

		metric := Metric{
			name:      "metric",
			fields:    fields,
			tags:      tags,
			timestamp: timestamp,
		}

		_, err = encoder.Encode(metric)
		if err != nil {
			return err
		}
	}
	return nil
}

type Metric struct {
	name      string
	tags      []*protocol.Tag
	fields    []*protocol.Field
	timestamp time.Time
}

func (m Metric) Time() time.Time {
	return m.timestamp
}
func (m Metric) Name() string {
	return m.name
}
func (m Metric) TagList() []*protocol.Tag {
	return m.tags
}
func (m Metric) FieldList() []*protocol.Field {
	return m.fields
}

func failOnError(err error, msg string) {
	if err != nil {
		log.Fatalf("%s: %s", msg, err)
	}
}

func fail(msg string) {
	log.Fatalf("%s", msg)
}

type variableFlags []string

func (i *variableFlags) String() string {
	return strings.Join(*i, ",")
}

func (i *variableFlags) Set(value string) error {
	*i = append(*i, value)
	return nil
}
func (i *variableFlags) Type() string {
	return "string"
}

func isFlagPassed(name string) bool {
	found := false
	flag.Visit(func(f *flag.Flag) {
		if f.Name == name {
			found = true
		}
	})
	return found
}
