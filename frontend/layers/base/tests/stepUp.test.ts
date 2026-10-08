import { describe, expect, it, vi } from "vitest";
import { ApiError, parseApiError } from "~/utils/apiError";
import { createStepUpPrompt, isStepUpRequired, STEP_UP_CODE } from "~/utils/stepUp";

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
