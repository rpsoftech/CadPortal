import { Routes } from '@angular/router';
import { authGuard, guestGuard, roleGuard } from './core/guards';

export const routes: Routes = [
  {
    path: 'login',
    title: 'Sign in',
    canActivate: [guestGuard],
    loadComponent: () => import('./features/login/login.component').then((m) => m.LoginComponent),
  },
  {
    path: 'forgot-password',
    title: 'Forgot password',
    canActivate: [guestGuard],
    loadComponent: () =>
      import('./features/password-reset/forgot-password.component').then(
        (m) => m.ForgotPasswordComponent,
      ),
  },
  {
    // Open to everyone: the link in the email may be opened in a browser where someone else is signed in.
    path: 'reset-password',
    title: 'Choose a new password',
    loadComponent: () =>
      import('./features/password-reset/reset-password.component').then(
        (m) => m.ResetPasswordComponent,
      ),
  },
  {
    path: '',
    canActivate: [authGuard],
    children: [
      {
        path: '',
        pathMatch: 'full',
        title: 'Catalog',
        loadComponent: () =>
          import('./features/catalog/catalog.component').then((m) => m.CatalogComponent),
      },
      {
        path: 'designs/:code',
        title: 'Design',
        loadComponent: () =>
          import('./features/design-detail/design-detail.component').then(
            (m) => m.DesignDetailComponent,
          ),
      },
      {
        path: 'designs/:code/variations/:variationId/edit',
        title: 'Edit variation',
        canActivate: [roleGuard('admin', 'designer')],
        loadComponent: () =>
          import('./features/variation-editor/variation-editor.component').then(
            (m) => m.VariationEditorComponent,
          ),
      },
      {
        path: 'master-data',
        title: 'Master data',
        canActivate: [roleGuard('admin')],
        loadComponent: () =>
          import('./features/master-data/master-data.component').then((m) => m.MasterDataComponent),
      },
      {
        path: 'removed',
        title: 'Removed',
        canActivate: [roleGuard('admin', 'designer')],
        loadComponent: () =>
          import('./features/removed/removed.component').then((m) => m.RemovedComponent),
      },
      {
        path: 'costing-sheets',
        title: 'Costing sheets',
        canActivate: [roleGuard('admin', 'designer')],
        loadComponent: () =>
          import('./features/costing-sheets/costing-sheets.component').then(
            (m) => m.CostingSheetsComponent,
          ),
      },
      {
        path: 'costing-sheets/:id',
        title: 'Costing sheet',
        canActivate: [roleGuard('admin', 'designer')],
        loadComponent: () =>
          import('./features/costing-sheets/costing-sheet-detail.component').then(
            (m) => m.CostingSheetDetailComponent,
          ),
      },
      {
        path: 'audit',
        title: 'Audit log',
        canActivate: [roleGuard('admin')],
        loadComponent: () =>
          import('./features/audit/audit.component').then((m) => m.AuditComponent),
      },
      {
        path: 'backups',
        title: 'Backups',
        canActivate: [roleGuard('admin')],
        loadComponent: () =>
          import('./features/backups/backups.component').then((m) => m.BackupsComponent),
      },
      {
        path: 'users',
        title: 'Users',
        canActivate: [roleGuard('admin')],
        loadComponent: () =>
          import('./features/users/users.component').then((m) => m.UsersComponent),
      },
    ],
  },
  { path: '**', redirectTo: '' },
];
