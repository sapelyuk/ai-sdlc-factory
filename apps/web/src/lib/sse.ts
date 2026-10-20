export function connectSSE(
  url: string,
  onMessage: (data: string) => void,
): AbortController {
  const controller = new AbortController()
  const events = new EventSource(url)

  events.onmessage = (event: MessageEvent) => {
    onMessage(event.data)
  }

  events.onerror = () => {
    if (controller.signal.aborted) {
      events.close()
    }
  }

  controller.signal.addEventListener('abort', () => {
    events.close()
  })

  return controller
}
