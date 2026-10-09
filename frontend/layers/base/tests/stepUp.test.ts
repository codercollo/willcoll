import { describe, expect, it, vi } from "vitest";
import { ApiError, parseApiError } from "~/utils/apiError";
import {
  createStepUpPrompt,
  isStepUpRequired,
  STEP_UP_CANCELLED_MESSAGE,
  STEP_UP_CODE,
  withStepUp,
} from "~/utils/stepUp";

// Authorization.md D5: the API answers 403 + code step_up_required and the
// client asks for the password once, then retries.
describe("isStepUpRequired", () => {
  it("recognises the API's step-up response", () => {
    const err = parseApiError(403, { error: "re-enter your password to continue", code: STEP_UP_CODE });
    expect(isStepUpRequired(err)).toBe(true);
  });

  it("is not fooled by an ordinary 403 (no permission)", () => {
    const err = parseApiError(403, { error: "your account doesn't have permission to access this resource" });
    expect(isStepUpRequired(err)).toBe(false);
  });

  it("needs the 403 status as well as the code", () => {
    expect(isStepUpRequired(new ApiError(422, "x", {}, null, STEP_UP_CODE))).toBe(false);
  });

  it("ignores things that are not API errors", () => {
    expect(isStepUpRequired(new Error("boom"))).toBe(false);
    expect(isStepUpRequired(null)).toBe(false);
  });
});

describe("createStepUpPrompt", () => {
  it("opens on ask and resolves true when the password was accepted", async () => {
    const onChange = vi.fn();
    const prompt = createStepUpPrompt(onChange);
    const answer = prompt.ask();
    expect(prompt.isOpen).toBe(true);
    expect(onChange).toHaveBeenLastCalledWith(true);

    prompt.done(true);
    await expect(answer).resolves.toBe(true);
    expect(prompt.isOpen).toBe(false);
    expect(onChange).toHaveBeenLastCalledWith(false);
  });

  it("resolves false when dismissed", async () => {
    const prompt = createStepUpPrompt(() => {});
    const answer = prompt.ask();
    prompt.done(false);
    await expect(answer).resolves.toBe(false);
  });

  it("shares one prompt between requests that fail together", async () => {
    const onChange = vi.fn();
    const prompt = createStepUpPrompt(onChange);
    const first = prompt.ask();
    const second = prompt.ask();
    expect(onChange).toHaveBeenCalledTimes(1); // one password box, not two

    prompt.done(true);
    await expect(Promise.all([first, second])).resolves.toEqual([true, true]);
  });

  it("can be asked again after it settled", async () => {
    const prompt = createStepUpPrompt(() => {});
    const first = prompt.ask();
    prompt.done(false);
    await first;

    const second = prompt.ask();
    expect(prompt.isOpen).toBe(true);
    prompt.done(true);
    await expect(second).resolves.toBe(true);
  });

  it("done with nothing open does nothing (a modal closing after success)", () => {
    const onChange = vi.fn();
    const prompt = createStepUpPrompt(onChange);
    prompt.done(false);
    expect(onChange).not.toHaveBeenCalled();
  });
});

describe("withStepUp", () => {
  const stepUpError = () => parseApiError(403, { error: "re-enter your password to continue", code: STEP_UP_CODE });

  it("passes a request that needs no step-up straight through, without a prompt", async () => {
    const ask = vi.fn();
    await expect(withStepUp(async () => "done", ask)).resolves.toBe("done");
    expect(ask).not.toHaveBeenCalled();
  });

  it("shows the prompt, then retries the original request exactly once", async () => {
    const attempt = vi.fn().mockRejectedValueOnce(stepUpError()).mockResolvedValueOnce("saved");
    const ask = vi.fn().mockResolvedValue(true);
    await expect(withStepUp(attempt, ask)).resolves.toBe("saved");
    expect(ask).toHaveBeenCalledTimes(1);
    expect(attempt).toHaveBeenCalledTimes(2);
  });

  it("does not loop when the retry is refused again", async () => {
    const attempt = vi.fn().mockRejectedValue(stepUpError());
    const ask = vi.fn().mockResolvedValue(true);
    await expect(withStepUp(attempt, ask)).rejects.toMatchObject({ status: 403, code: STEP_UP_CODE });
    expect(ask).toHaveBeenCalledTimes(1);
    expect(attempt).toHaveBeenCalledTimes(2);
  });

  it("leaves the action undone and says so when the box is cancelled", async () => {
    const attempt = vi.fn().mockRejectedValue(stepUpError());
    const ask = vi.fn().mockResolvedValue(false);
    const err = await withStepUp(attempt, ask).catch((e) => e);
    expect(attempt).toHaveBeenCalledTimes(1); // never retried
    expect(err).toBeInstanceOf(ApiError);
    expect(err.message).toBe(STEP_UP_CANCELLED_MESSAGE);
    expect(isStepUpRequired(err)).toBe(false); // nothing re-prompts for the cancellation itself
  });

  it("does not prompt for an ordinary 403 or for a disabled caller (pay page token)", async () => {
    const forbidden = parseApiError(403, { error: "no permission" });
    const ask = vi.fn();
    await expect(withStepUp(() => Promise.reject(forbidden), ask)).rejects.toBe(forbidden);
    const stepUp = stepUpError();
    await expect(withStepUp(() => Promise.reject(stepUp), ask, false)).rejects.toBe(stepUp);
    expect(ask).not.toHaveBeenCalled();
  });
});
