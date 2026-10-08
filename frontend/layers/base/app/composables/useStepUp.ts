import { createStepUpPrompt, type StepUpPrompt } from "../utils/stepUp";

// The one password prompt for step-up (utils/stepUp.ts). Client only: it is
// opened by a request that failed in response to something the user did.
let prompt: StepUpPrompt | null = null;

export function useStepUp() {
  const open = useState("stepUpOpen", () => false);
  prompt ??= createStepUpPrompt((v) => {
    open.value = v;
  });
  const shared = prompt;
  return {
    open,
    /** Show the prompt; true once the password was accepted, false if dismissed. */
    ask: () => shared.ask(),
    /** Close the prompt and release the requests waiting on it. */
    done: (ok: boolean) => shared.done(ok),
  };
}
