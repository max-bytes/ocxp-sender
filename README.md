# Introduction / Purpose

The main purpose of this executable (ocxp-sender) is to take performance data received from Naemon using the ochp/ocsp mechanism (https://www.naemon.org/documentation/usersguide/distributed.html), transform it into Influx Line Protocol (https://docs.influxdata.com/influxdb/v2.0/reference/syntax/line-protocol/), then hand it over an AMQP-supporting message queue (https://en.wikipedia.org/wiki/Advanced_Message_Queuing_Protocol), like RabbitMQ (https://www.rabbitmq.com/).

# Build and package

ocxp-sender is built and packaged for release using gitlab CI pipelines. See .gitlab-ci.yml for further info. To run ocxp-sender locally (during development), you can use `go run`.

# Commandline parameters

ocxp-sender includes the following commandline parameters:

|  | parameter | optional | description |
|-|-|-|-|
| host | &#x2011;h<br>--host | false | Hostname for which the performance data is reported |
| service description | -s<br>--service | false | Name of the service for which the performance data is reported |
| state | -t<br>--state | false | (Integer); state of the service, according to Naemon standard: https://www.naemon.org/documentation/usersguide/pluginapi.html#return_code |
| performance data | -p<br>--perfdata | false | The performance data as reported by naemon |
| AMQP URL | -u<br>--amqp-url | true | URL of the target AMQP (e.g. RabbitMQ), where the data should be sent to, defaults to amqp://localhost:5672 |
| variables | -v<br>--var | true | Variables in the form "name=value" (multiple -v allowed); get forwarded as tags |

# Influx Line Protocol

Whenever Naemon records a new check result, the ochp/ocxp handler is run, which in turn calls the ocxp-sender executable. A Naemon check result contains the corresponding host and service, the check's resulting state, and any number of performance data lines (can also be zero).

Each line in the performance data reported by Naemon is converted to one line(=measurement) in the Influx Line Protocol output. 
Additionally, the check result state is converted to an output line too.

Example output, from a host check result:
```
// Syntax: <measurement>[,<tag_key>=<tag_value>[,<tag_key>=<tag_value>]] <field_key>=<field_value>[,<field_key>=<field_value>] [<timestamp>]
value,host=abc.com,label=rta,service=CI-Alive,uom=ms,variable1=value1 value=1.238,warn=3000,crit=5000,min=0 1601368660199853426
value,host=abc.com,label=pl,service=CI-Alive,uom=%,variable1=value1 value=0,warn=80,crit=100,min=0 1601368660199886231
state,host=abc.com,label=state,service=CI-Alive,variable1=value1 value=0i 1601368660199896617
```

# Example naemon configuration
/etc/naemon/conf.d/commands/commands.cfg:
```
define command {
command_name ocsp_handler /data/ocxp-sender/ocxp-sender -h '$HOSTNAME$' -s '$SERVICEDESC$' -t $SERVICESTATEID$ -p '$SERVICEPERFDATA$' -v tag1=value1
}
define command {
command_name ochp_handler
command_line /data/ocxp-sender/ocxp-sender -h '$HOSTNAME$' -s 'CI-Alive' -t $HOSTSTATEID$ -p '$HOSTPERFDATA$' -v tag1=value1
}
```

/etc/naemon/naemon.cfg:
```
obsess_over_services=1
ocsp_command=ocsp_handler
obsess_over_hosts=1
ochp_command=ochp_handler
```