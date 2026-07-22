package http

import _ "embed"

//go:embed openapi.yaml
var openAPISpec []byte

var scalarHTML = []byte(`<!DOCTYPE html>
<html>
  <head>
    <title>Order Service API</title>
    <meta charset="utf-8"/>
    <meta name="viewport" content="width=device-width, initial-scale=1">
  </head>
  <body>
    <script id="api-reference" data-url="/docs/openapi.yaml"
      src="https://cdn.jsdelivr.net/npm/@scalar/api-reference@1.25.130/dist/browser/standalone.min.js"
      integrity="sha384-oyL8y2b0EvVxYsvg2qNlT8xHcsvmySaljIklWvbATuTrccZSyvWnsPnmOunYQL2R"
      crossorigin="anonymous"></script>
  </body>
</html>`)
