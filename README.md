# go-elector

Leader election based on leader lease approach. Requires Consul.

## API

Representation of the elector instance (this type is exported only for better
docs, could have kept private):

```go
type Instance
```

Type of function called when leader changes:

```go
type Callback func(oldLeaderId string, newLeaderId string)
```

Create a new instance of the elector:

```go
func Create(selfId string, consulUrl string, leaderHoldTime time.Duration) (inst *Instance, err error)
```

Determine current leader id, returns '' if leader is unknown:

```go
func (inst *Instance) GetCurrentLeader() (leaderid string)
```

Registers a callback:

```go
func (inst *Instance) RegisterCallback(cb Callback)
```

## Example

```go
package main

import (
    "flag"
    "github.com/afiskon/go-elector"
    "log"
    "time"
)

func main() {
    selfIdPtr := flag.String("uniqid", "", "Unique id of this node")
    flag.Parse()

    consulUrl := "http://localhost:8500/v1/kv/test/leader_election"
    electorInst, err := elector.Create(*selfIdPtr, consulUrl, 15*time.Second)
    if err != nil {
        log.Panicf("Unable to create the elector: %s", err.Error())
    }

    electorInst.RegisterCallback(func(oldLeaderId string, newLeaderId string) {
        log.Printf("Leader changed: '%s' -> '%s'\n", oldLeaderId, newLeaderId)
    })

    for {
        leaderId := electorInst.GetCurrentLeader()
        log.Printf("Current leader: '%s'\n", leaderId)
        time.Sleep(5 * time.Second)
    }
}
```

Usage:

```bash
./leader-elect -uniqid archlinux1
```
