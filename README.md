# Write-Ahead Logs Implementations

A repo with implementation with WAL implementations for reference and comparisons

## Contents:
- `main.go`: drive code for running the demo test.
- `common`: common interface for all WAL implementations
- `singlefile`: a single log file WAL implementation
- `segmented`: a multiple segmented logs files WAL implementation

## How to run:

## Running 

```powershell
go run main.go
```

### Benchmarking
```powershell
go test ./bench/... -bench "." -benchmem
```

## References
- https://github.com/tidwall/wal