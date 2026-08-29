package main

import (
	"flag"
	"fmt"
	"main/segmented"
	"main/singlefile"
)

func main() {
	variant := flag.String("variant", "", "which WAL variant to demo")
	flag.Parse()

	switch *variant {
	case "singlefile":
		singlefile.RunDemo()
	case "segmented":
		segmented.RunDemo()
	default:
		singlefile.RunDemo()
		fmt.Println("---------------------")
		segmented.RunDemo()
	}
}
