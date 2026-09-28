import '@testing-library/jest-dom/vitest';

// jsdom non ha ResizeObserver (lo usano le primitive Radix, es. Select).
class ResizeObserverStub {
  observe() {}
  unobserve() {}
  disconnect() {}
}
globalThis.ResizeObserver ??= ResizeObserverStub;
