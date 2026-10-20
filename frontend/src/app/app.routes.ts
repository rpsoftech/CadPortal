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
        path: 'audit',
        title: 'Audit log',
        canActivate: [roleGuard('admin')],
        loadComponent: () =>
          import('./features/audit/audit.component').then((m) => m.AuditComponent),
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
