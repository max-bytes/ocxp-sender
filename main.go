package main

import (
	"fmt"
	"log"
	"bytes"
	"strconv"
	"time"
	"os/exec"
	"os"
	"net"
	"bufio"
	"strings"
	flag "github.com/spf13/pflag"
	"regexp"
	"github.com/streadway/amqp"
	"github.com/influxdata/line-protocol"
)

const QueueName = "naemon"
const DaemonAddress = "127.0.0.1:55550"

func main() {
	var host string
	var service string
	var state int
	var variableFlags variableFlags
	var perfData string
	var daemonize bool
	var amqpURL string
	flag.VarP(&variableFlags, "var", "v", "variables in the form \"name=value\" (multiple -v allowed); get forwarded as tags")
	flag.StringVarP(&host, "host", "h", "", "Name of host")
	flag.StringVarP(&service, "service", "s", "", "Name of service")
	flag.IntVarP(&state, "state", "t", 0, "State of the check")
	flag.StringVarP(&perfData, "perfdata", "p", "", "Performance data")
	flag.StringVarP(&amqpURL, "amqp-url", "u", "amqp://localhost:5672", "URL of the AMQP (e.g. RabbitMQ) server to send the data to")
	flag.BoolVarP(&daemonize, "daemonize", "d", false, "Whether or not to spawn a daemon process that runs infinitely")
	flag.Parse()

	if daemonize { // run as daemon
		fmt.Println("Running daemon...")
		runDaemon(DaemonAddress, amqpURL, 6 * time.Minute)
		fmt.Println("Stopping daemon")
	} else { // run as regular program that sends its metrics to the daemon
		if !isFlagPassed("host") { fail("host name not set") }
		if !isFlagPassed("service") { fail("service name not set") }
		if !isFlagPassed("state") { fail("state not set") }
		if !isFlagPassed("perfdata") { fail("Performance data not set") }
	
		b, err := parse(host, service, state, variableFlags, perfData)
		failOnError(err, "Failed to parse inputs")

		// only publish if there are actually metrics/perfdata
		if b.Len() > 0 {
			fmt.Println("Sending:")
			fmt.Println(b.String());

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
				_, err = os.StartProcess(binary, []string{binary, "-d", "-u", amqpURL}, &os.ProcAttr{Dir: "", Env: nil,
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
	const maxBufferSize = 16384

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
	queue, err := channel.QueueDeclare(
		QueueName, // name
		true,   // durable
		false,   // delete when unused
		false,   // exclusive
		false,   // no-wait
		nil,     // arguments
	)
	failOnError(err, "Failed to declare a queue")

	doneChan := make(chan error, 1)
	heartbeatChan := make(chan bool, 1)

	go func() {
		buffer := make([]byte, maxBufferSize)

		// var i = 0

		for {
			conn, err := connection.Accept()
			if err != nil {
				doneChan <- err
				return
			}

			n, err := bufio.NewReaderSize(conn, maxBufferSize).Read(buffer)
			if err != nil {
				doneChan <- err
				return
			}

			received := buffer[:n]
			
			err = publish(channel, queue, received)
			if err != nil {
				doneChan <- err
				return
			}

			// fmt.Println(i)
			// i++

			heartbeatChan <- true
		}
	}()

	// TODO: add signal handling to allow graceful exit
	L:
	for {
		select {
		case err = <-doneChan:
			failOnError(err, "Encountered error while processing message")
		case _ = <-heartbeatChan: // heartbeat encountered, continue loop and restart select
		case <-time.After(inactivityTimeout):
			fmt.Println("Reached inactivity timeout, closing...")
			break L
		}
	}
}

func parse(host string, service string, state int, variableFlags variableFlags, perfData string) (*bytes.Buffer, error) {
	// create tags from variables
    inputTags := make(map[string]string)
    for _, item := range variableFlags {
		x := strings.SplitN(item, "=", 2)
		if (len(x) < 2) { 
			return nil, fmt.Errorf("variable %v could not be parsed into name=value", item)
		}
        inputTags[x[0]] = x[1]
    }
	
	var b bytes.Buffer
	encoder := protocol.NewEncoder(&b)

	tags := map[string]string {
		"host": host,
		"service": service,
	}
	// add input tags
	for k, v := range inputTags {
		tags[k] = v
	}

	for pd := range parsePerfData(perfData) {
		metric := perfData2metric("value", pd, tags)
		_, err := encoder.Encode(metric)
		if err != nil {
			return nil, err
		}
	}

	// add state as its own metric(?), with the state encoded as an integer (0 to 3)
	metric := perfData2metric("value", PerfData{
		Key: "state",
		Value: state,
	}, tags)
	
	_, err := encoder.Encode(metric)
	if err != nil {
		return nil, err
	}

	return &b, nil
}

func publish(channel *amqp.Channel, queue amqp.Queue, msg []byte) error {
	return channel.Publish(
		"",     // exchange
		queue.Name, // routing key
		false,  // mandatory
		false,  // immediate
		amqp.Publishing {
		  ContentType: "text/plain",
		  Body:        msg,
		  DeliveryMode: 2,
		})
}

func perfData2metric(metricName string, pd PerfData, addedTags map[string]string) Metric {
	var fields = []*protocol.Field {
		&(protocol.Field { Key: "value", Value: pd.Value }),
	}
	if pd.Warn != nil {
		fields = append(fields, &(protocol.Field { Key: "warn", Value: *pd.Warn }))
	}
	if pd.Crit != nil {
		fields = append(fields, &(protocol.Field { Key: "crit", Value: *pd.Crit }))
	}
	if pd.Min != nil {
		fields = append(fields, &(protocol.Field { Key: "min", Value: *pd.Min }))
	}
	if pd.Max != nil {
		fields = append(fields, &(protocol.Field { Key: "max", Value: *pd.Max }))
	}

	var tags = []*protocol.Tag {
		&(protocol.Tag { Key: "label", Value: pd.Key }),
	}
	for key, value := range addedTags {
		tags = append(tags, &(protocol.Tag { Key: key, Value: value }))
	}

	// add UOM, if present
	if pd.UOM != nil {
		tags = append(tags, &(protocol.Tag { Key: "uom", Value: *pd.UOM }))
	}

	metric := Metric {
		name: metricName,
		fields: fields,
		tags: tags,
	}

	return metric
}

type PerfData struct {
	Key string
	Value interface{}
	UOM *string
	Warn *float64
	Crit *float64
	Min *float64
	Max *float64
}

// partly taken from https://github.com/Griesbacher/nagflux/blob/ea877539bc49ed67e9a5e35b8a127b1ff4cadaad/collector/spoolfile/nagiosSpoolfileWorker.go
var regexPerformanceLabel = regexp.MustCompile(`([^=]+)=(U|[\d\.,\-]+)([\pL\/%]*);?([\d\.,\-:~@]+)?;?([\d\.,\-:~@]+)?;?([\d\.,\-]+)?;?([\d\.,\-]+)?;?\s*`)
func parsePerfData(str string) <-chan PerfData {
	ch := make(chan PerfData)
	go func() {
		perfSlice := regexPerformanceLabel.FindAllStringSubmatch(str, -1)

		for _, value := range perfSlice {
			v, err := strconv.ParseFloat(value[2], 64)
			if err != nil {
				fmt.Println(err)
				continue
			}
			var uom *string = nil
			if value[3] != "" {
				uom = &(value[3])
			}
			var warn *float64 = nil
			warnF, err := strconv.ParseFloat(value[4], 64)
			if err == nil {
				warn = &warnF
			}
			var crit *float64 = nil
			critF, err := strconv.ParseFloat(value[5], 64)
			if err == nil {
				crit = &critF
			}
			var min *float64 = nil
			minF, err := strconv.ParseFloat(value[6], 64)
			if err == nil {
				min = &minF
			}
			var max *float64 = nil
			maxF, err := strconv.ParseFloat(value[7], 64)
			if err == nil {
				max = &maxF
			}
			perf := PerfData{
				Key: value[1],
				Value: v,
				UOM: uom,
				Warn: warn,
				Crit: crit,
				Min: min,
				Max: max,
			}
			ch <- perf
		}
		close(ch)
	}()

	return ch
}

type Metric struct {
	name string
	tags []*protocol.Tag
	fields []*protocol.Field
}
func (m Metric) Time() time.Time {
    return time.Now()
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
