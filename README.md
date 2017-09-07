# go-elector

Leader election based on leader lease approach. Requires Consul.

## API

```go
type Instance
```

Representation of the elector instance. This type is exported only for better
docs, could have kept private.

```go
type Callback func(oldLeaderId string, newLeaderId string)
```

Type of function called when leader changes.

```go
func Create(selfId string, consulUrl string, leaderHoldTime time.Duration) (inst *Instance, err error)
```

Create an instance of the elector.

```go
func (inst *Instance) GetCurrentLeader() (leaderid string)
```

Returns current leader id, or '' if leader is unknown.

```go
func (inst *Instance) RegisterCallback(cb Callback)
```

Registers a callback.

## Example

TODO
