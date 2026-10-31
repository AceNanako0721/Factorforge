import { fail } from "../api/protocol.js";
import { validateBudget } from "./request-budget.js";

export const inferenceFields = ["timeout_seconds", "max_input_bytes", "max_output_bytes", "max_line_bytes", "budget_window_seconds", "omp_max_requests"] as const;
export type InferenceField = typeof inferenceFields[number];
export type InferenceLimits = Record<InferenceField, number>;
export function parseInferenceValue(field: InferenceField, input: string, current: number) {
  const value = input.trim() === "" ? current : /^[0-9]+$/.test(input.trim()) ? Number(input.trim()) : NaN;
  const optional = field === "budget_window_seconds" || field === "omp_max_requests";
  if (!Number.isSafeInteger(value) || value < 0 || !optional && value === 0) fail("LIMITS_INVALID");
  return value;
}
export function validateInferenceLimits(values: InferenceLimits) {
  for (const field of inferenceFields) parseInferenceValue(field, String(values[field]), 0);
  if (values.timeout_seconds * 1000 > 2147483647 || values.budget_window_seconds * 1000 > Number.MAX_SAFE_INTEGER) fail("LIMITS_INVALID");
  validateBudget(values.omp_max_requests, values.budget_window_seconds);
}
