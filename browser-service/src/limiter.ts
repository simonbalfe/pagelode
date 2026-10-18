import { ServiceError } from "./contract.ts";

export class Limiter {
  readonly #concurrency: number;
  readonly #maxWaiting: number;
  readonly #queue: Array<() => void> = [];
  #active = 0;

  constructor(concurrency: number, maxWaiting: number) {
    this.#concurrency = concurrency;
    this.#maxWaiting = maxWaiting;
  }

  get active(): number {
    return this.#active;
  }

  get waiting(): number {
    return this.#queue.length;
  }

  async run<T>(signal: AbortSignal, task: () => Promise<T>): Promise<T> {
    await this.#acquire(signal);
    try {
      return await task();
    } finally {
      this.#active -= 1;
      this.#queue.shift()?.();
    }
  }

  #acquire(signal: AbortSignal): Promise<void> {
    signal.throwIfAborted();
    if (this.#active < this.#concurrency) {
      this.#active += 1;
      return Promise.resolve();
    }
    if (this.#queue.length >= this.#maxWaiting) {
      return Promise.reject(new ServiceError(429, "saturated", "browser capacity and queue are full"));
    }
    const { promise, resolve, reject } = Promise.withResolvers<void>();
    const grant = (): void => {
      signal.removeEventListener("abort", abandon);
      this.#active += 1;
      resolve();
    };
    const abandon = (): void => {
      const index = this.#queue.indexOf(grant);
      if (index !== -1) this.#queue.splice(index, 1);
      reject(signal.reason);
    };
    this.#queue.push(grant);
    signal.addEventListener("abort", abandon, { once: true });
    return promise;
  }
}
