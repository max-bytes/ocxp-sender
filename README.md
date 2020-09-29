# Introduction / Purpose

The main purpose of this executable (ocxp-sender) is to take performance data received from Naemon using the ochp/ocsp mechanism (https://www.naemon.org/documentation/usersguide/distributed.html), transform it into Influx Line Protocol (https://docs.influxdata.com/influxdb/v2.0/reference/syntax/line-protocol/), then hand it over an AMQP-supporting message queue (https://en.wikipedia.org/wiki/Advanced_Message_Queuing_Protocol), like RabbitMQ (https://www.rabbitmq.com/).

# Commandline parameters

ocxp-sender includes the following commandline parameters:

|  | parameter | optional | description |
|-|-|-|-|
| hostname | &#x2011;h<br>&#x2011;&#x2011;hostname | false | Hostname for which the performance data is reported |
| service description | -d<br>--desc | false | Service description for which the performance data is reported |
| state | -s<br>--state | false | (Integer); state of the service, according to Naemon standard: https://www.naemon.org/documentation/usersguide/pluginapi.html#return_code |
| performance data | -p<br>--perfdata | false | The performance data as reported by naemon |
| AMQP URL | -u<br>--amqp-url | true | URL of the target AMQP (e.g. RabbitMQ), where the data should be sent to, defaults to amqp://localhost:5672 |
| variables | -v<br>--var | true | variables in the form "name=value" (multiple -v allowed); get forwarded as tags |

# Example naemon configuration
/etc/naemon/conf.d/commands/commands.cfg:
```
define command {
command_name ocsp_handler /data/ocxp-sender/ocxp-sender -h '$HOSTNAME$' -d '$SERVICEDESC$' -s $SERVICESTATEID$ -p '$SERVICEPERFDATA$' -v tag1=value1
}
define command {
command_name ochp_handler
command_line /data/ocxp-sender/ocxp-sender -h '$HOSTNAME$' -d 'CI-Alive' -s $HOSTSTATEID$ -p '$HOSTPERFDATA$' -v tag1=value1
}
```

/etc/naemon/naemon.cfg:
```
obsess_over_services=1
ocsp_command=ocsp_handler
obsess_over_hosts=1
ochp_command=ochp_handler
```