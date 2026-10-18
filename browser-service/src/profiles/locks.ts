import { ServiceError } from "../shared/errors.ts";

export class ProfileLocks {
  readonly #renders = new Map<string, Promise<void>>();
  readonly #sessions = new Set<string>();

  async withRender<T>(name: string, task: () => Promise<T>): Promise<T> {
    if (this.#sessions.has(name)) throw new ServiceError(409, "profile_in_use", `profile ${JSON.stringify(name)} is held by an open session`);
    const previous = this.#renders.get(name) ?? Promise.resolve();
    const { promise: done, resolve: release } = Promise.withResolvers<void>();
    const tail = previous.then(() => done);
    this.#renders.set(name, tail);
    await previous;
    try {
      return await task();
    } finally {
      release();
      if (this.#renders.get(name) === tail) this.#renders.delete(name);
    }
  }

  holdForSession(name: string): void {
    if (this.#sessions.has(name) || this.#renders.has(name)) throw new ServiceError(409, "profile_in_use", `profile ${JSON.stringify(name)} is in use`);
    this.#sessions.add(name);
  }

  releaseSession(name: string): void {
    this.#sessions.delete(name);
  }
}
