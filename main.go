package main

import (
	"fmt"

	"github.com/docker/go-plugins-helpers/volume"
)

func main() {
	driver := NewLvmPersistDriver()

	handler := volume.NewHandler(driver)
	fmt.Println(handler.ServeUnix("root", driver.Name))
}
