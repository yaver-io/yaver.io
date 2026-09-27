param(
  [Parameter(Mandatory = $true)]
  [string]$ExpressionBase64,
  [int]$Port = 9222
)

$ErrorActionPreference = "Stop"
$expression = [Text.Encoding]::UTF8.GetString([Convert]::FromBase64String($ExpressionBase64))
$targets = Invoke-RestMethod "http://127.0.0.1:$Port/json/list"
$target = $targets |
  Where-Object { $_.type -eq "page" -and $_.url -match "/dashboard" } |
  Select-Object -First 1
if (-not $target) {
  throw "No Yaver dashboard DevTools target was found."
}

$socket = [Net.WebSockets.ClientWebSocket]::new()
$cancellation = [Threading.CancellationToken]::None
$socket.ConnectAsync([Uri]$target.webSocketDebuggerUrl, $cancellation).GetAwaiter().GetResult()
try {
  $request = @{
    id = 1
    method = "Runtime.evaluate"
    params = @{
      expression = $expression
      awaitPromise = $true
      returnByValue = $true
      userGesture = $true
    }
  } | ConvertTo-Json -Depth 8 -Compress
  $requestBytes = [Text.Encoding]::UTF8.GetBytes($request)
  $requestSegment = [ArraySegment[byte]]::new($requestBytes)
  $socket.SendAsync(
    $requestSegment,
    [Net.WebSockets.WebSocketMessageType]::Text,
    $true,
    $cancellation
  ).GetAwaiter().GetResult()

  while ($true) {
    $stream = [IO.MemoryStream]::new()
    try {
      do {
        $buffer = New-Object byte[] 65536
        $segment = [ArraySegment[byte]]::new($buffer)
        $received = $socket.ReceiveAsync($segment, $cancellation).GetAwaiter().GetResult()
        if ($received.MessageType -eq [Net.WebSockets.WebSocketMessageType]::Close) {
          throw "DevTools closed the socket before returning a result."
        }
        $stream.Write($buffer, 0, $received.Count)
      } while (-not $received.EndOfMessage)

      $message = [Text.Encoding]::UTF8.GetString($stream.ToArray()) | ConvertFrom-Json
      if ($message.id -eq 1) {
        $message | ConvertTo-Json -Depth 20 -Compress
        break
      }
    } finally {
      $stream.Dispose()
    }
  }
} finally {
  if ($socket.State -eq [Net.WebSockets.WebSocketState]::Open) {
    $socket.CloseAsync(
      [Net.WebSockets.WebSocketCloseStatus]::NormalClosure,
      "done",
      $cancellation
    ).GetAwaiter().GetResult()
  }
  $socket.Dispose()
}
