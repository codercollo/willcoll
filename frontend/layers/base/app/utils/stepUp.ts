import { ApiError } from "./apiError";

// Step-up authentication (docs/security/authorization.md, D5). Money-moving
// actions (a payment channel change, a reversal, a staff authority change)
// answer 403 with this code when the session has not re-entered its password
// in the last five minutes. useApi then asks for the password once, opens the
// window through platform-api and retries the request, so no screen has to
// handle it itself.
export const STEP_UP_CODE = "step_up_required";

/** True for the 403 that means "ask for the password and retry". */
export function isStepUpRequired(err: unknown): err is ApiError {
  return err instanceof ApiError && err.status === 403 && err.code === STEP_UP_CODE;
}

export interface StepUpPrompt {
  /** Opens the prompt; resolves true once the password was accepted, false if dismissed. */
  ask(): Promise<boolean>;
  /** Closes the prompt and settles everyone waiting on it. A no-op when none is open. */
  done(ok: boolean): void;
  readonly isOpen: boolean;
}

/**
 * One prompt shared by every request: several calls failing at the same time
 * (a form that saves two things) wait on a single password box instead of
 * stacking several. onChange tells the UI when to show or hide it.
 */
export function createStepUpPrompt(onChange: (open: boolean) => void): StepUpPrompt {
  let pending: { promise: Promise<boolean>; settle: (ok: boolean) => void } | null = null;
  return {
    ask() {
      if (pending) return pending.promise;
      let settle!: (ok: boolean) => void;
      const promise = new Promise<boolean>((resolve) => {
        settle = resolve;
      });
      pending = { promise, settle };
      onChange(true);
      return promise;
    },
    done(ok) {
      if (!pending) return;
      const { settle } = pending;
      pending = null;
      onChange(false);
      settle(ok);
    },
    get isOpen() {
      return pending !== null;
    },
  };
}

/** Shown when the password box is dismissed: the action was NOT performed. */
export const STEP_UP_CANCELLED_MESSAGE =
  "You didn't confirm your password, so nothing was changed.";

/** The error a dismissed password box leaves behind. It carries no step-up code, so nothing re-prompts for it. */
export function stepUpCancelledError(): ApiError {
  return new ApiError(403, STEP_UP_CANCELLED_MESSAGE);
}

/**
 * Runs attempt; if the API answers "step up required", asks for the password
 * once and retries the SAME request exactly once. A dismissed box leaves the
 * action undone and throws stepUpCancelledError; a retry that is refused again
 * surfaces that refusal as it is (no loop). Anything else passes through.
 */
export async function withStepUp<T>(
  attempt: () => Promise<T>,
  ask: () => Promise<boolean>,
  enabled = true,
): Promise<T> {
  try {
    return await attempt();
  } catch (err) {
    if (!enabled || !isStepUpRequired(err)) throw err;
    if (!(await ask())) throw stepUpCancelledError();
    return await attempt();
  }
}
