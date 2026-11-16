import { HttpErrorResponse, HttpInterceptorFn } from '@angular/common/http';
import { inject } from '@angular/core';
import { catchError, throwError } from 'rxjs';
import { AuthService } from './auth.service';

/**
 * Adds the bearer token to same-origin API calls. A 401 on any call except login
 * itself (where it just means wrong credentials) ends the session and returns the
 * user to the login page.
 */
export const authInterceptor: HttpInterceptorFn = (req, next) => {
  const auth = inject(AuthService);
  if (!req.url.startsWith('/api/')) return next(req);

  const token = auth.token();
  const authed = token ? req.clone({ setHeaders: { Authorization: `Bearer ${token}` } }) : req;
  return next(authed).pipe(
    catchError((err: unknown) => {
      if (
        err instanceof HttpErrorResponse &&
        err.status === 401 &&
        !req.url.endsWith('/auth/login')
      ) {
        // The server ends a login that made no request for a while and says so; show the same
        // "signed out after inactivity" message as the browser's own idle sign-out.
        if ((err.error as { code?: string } | null)?.code === 'idle_timeout') auth.expireIdle(true);
        else auth.expire();
      }
      return throwError(() => err);
    }),
  );
};
