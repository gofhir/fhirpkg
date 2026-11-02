package main

import (
	"net/http"
	"time"
)

// httpClient tiene un plazo por petición; las descargas grandes (núcleos de
// 17 MB) caben de sobra.
var httpClient = http.Client{Timeout: 10 * time.Minute}
