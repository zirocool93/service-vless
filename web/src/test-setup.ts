import '@testing-library/jest-dom/vitest'

class StubEventSource {
  onmessage: ((event: MessageEvent<string>) => void) | null = null
  close() {}
}

Object.defineProperty(globalThis, 'EventSource', { writable: true, value: StubEventSource })
