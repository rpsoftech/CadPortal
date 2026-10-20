import { AbstractControl, ValidationErrors, ValidatorFn } from '@angular/forms';

/** A positive decimal with at most `maxDp` decimal places, e.g. "200", "3.55". Empty passes. */
export function positiveDecimal(maxDp: number): ValidatorFn {
  const re = new RegExp(`^\\d+(\\.\\d{1,${maxDp}})?$`);
  return (c: AbstractControl): ValidationErrors | null => {
    const v = `${c.value ?? ''}`.trim();
    if (v === '') return null;
    if (!re.test(v)) return { decimal: `Up to ${maxDp} decimal places` };
    return Number(v) > 0 ? null : { positive: 'Must be greater than 0' };
  };
}

/** A whole number >= 1. Empty passes. */
export const positiveInt: ValidatorFn = (c) => {
  const v = `${c.value ?? ''}`.trim();
  if (v === '') return null;
  return /^\d+$/.test(v) && Number(v) >= 1 ? null : { int: 'Whole number, 1 or more' };
};

/** Password length limits; the server enforces the same policy. */
export const PASSWORD_MIN = 12;
export const PASSWORD_MAX = 128;
