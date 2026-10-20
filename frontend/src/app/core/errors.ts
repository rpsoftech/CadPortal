import { HttpErrorResponse } from '@angular/common/http';
import { ApiErrorBody } from './models';

export interface ApiError {
  status: number;
  message: string;
  /** (size, code) pairs the API says are missing, from 422 responses. */
  missing: { SizeMM: string; Code: string }[];
  metalsMissingWastage: string[];
}

/**
 * Normalises any HTTP failure into a readable message. Handles blob bodies too
 * (export endpoints use responseType: 'blob', so their JSON errors arrive as Blobs).
 */
export async function toApiError(err: unknown): Promise<ApiError> {
  if (!(err instanceof HttpErrorResponse)) {
    return { status: 0, message: 'Unexpected error', missing: [], metalsMissingWastage: [] };
  }
  let body: ApiErrorBody | string | null = err.error;
  if (body instanceof Blob) {
    try {
      body = JSON.parse(await body.text()) as ApiErrorBody;
    } catch {
      body = null;
    }
  }
  const obj = body && typeof body === 'object' ? body : null;
  let message = obj?.error;
  if (!message) {
    message =
      err.status === 0
        ? 'Cannot reach the API. Is the backend running?'
        : err.status === 401
          ? 'Sign in with a valid token to do this.'
          : err.status === 403
            ? 'Your role is not permitted to do this.'
            : err.status === 503
              ? 'File storage is not configured on the server.'
              : `Request failed (${err.status})`;
  }
  return {
    status: err.status,
    message,
    missing: obj?.missing ?? obj?.stone_rates_missing ?? [],
    metalsMissingWastage: obj?.metals_missing_wastage ?? [],
  };
}
