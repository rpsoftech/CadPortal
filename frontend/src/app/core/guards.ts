import { inject } from '@angular/core';
import { CanActivateFn, Router } from '@angular/router';
import { AuthService, Role } from './auth.service';

/** Requires a signed-in user whose session the server still accepts. */
export const authGuard: CanActivateFn = async (_route, state) => {
  const auth = inject(AuthService);
  const router = inject(Router);
  const toLogin = () => router.createUrlTree(['/login'], { queryParams: { returnUrl: state.url } });
  if (!auth.isAuthenticated()) return toLogin();
  return (await auth.verifyOnce()) ? true : toLogin();
};

/** Requires one of the given roles (the server enforces the same rule). */
export const roleGuard =
  (...roles: Role[]): CanActivateFn =>
  () => {
    const auth = inject(AuthService);
    const role = auth.role();
    return role !== null && roles.includes(role) ? true : inject(Router).createUrlTree(['/']);
  };

/** Keeps signed-in users off the login page. */
export const guestGuard: CanActivateFn = () => {
  const auth = inject(AuthService);
  return auth.isAuthenticated() ? inject(Router).createUrlTree(['/']) : true;
};

/**
 * Only same-app paths may be used as a post-login destination, so a crafted link
 * cannot send someone to another site ("//evil.test", "https://...", "/\evil.test").
 */
export function safeReturnUrl(url: string | null | undefined): string {
  if (!url || !url.startsWith('/') || url.startsWith('//') || url.includes('\\')) return '/';
  if (url.startsWith('/login')) return '/';
  return url;
}
