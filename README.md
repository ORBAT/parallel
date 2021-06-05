See [godoc](https://pkg.go.dev/github.com/ORBAT/parallel). 

Allows easy parallelization of a known number of functions and waiting for them to finish.The
examples show how you can return results from goroutines without having to use channels or locking.
The general idea is to pre-reserve a slice for them and have each goroutine write into its own index
in the slice. This means that you're possibly using more memory, but depending on how many things
you're actually doing in parallel and what sort of results you're returning, this method will
actually perform better than more complex solutions.

```go
	 	// reserve enough space for the results
		results := make([]resultType, len(input))

		// this is the function we want to parallelize.
		// idx will go from 0 to len(input) (see Do call below)
		fn := Func(func(idx int) error {
			// no mutex needed because every goroutine gets a unique idx, so writes never overlap
			results[idx], err := doSomethingCPUIntensiveTo(input[idx])


			return err
		})

		// run fn in parallel len(input) times, with at most GOMAXPROCS at a time.
		err := fn.Do(len(input))

		for i, r := range results {
			// do something to all the results
		}
```