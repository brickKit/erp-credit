// Command erp-credit runs the credit component alone (serve, migrate); the shells compile in credit.Definition().
package main

import (
	"github.com/brickKit/be-sdk-go/v3/launcher"
	"github.com/brickKit/erp-credit/v3/credit"
)

func main() { launcher.Main(credit.Definition()) }
