import { Routes } from '@angular/router';

export const routes: Routes = [
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
    loadComponent: () =>
      import('./features/variation-editor/variation-editor.component').then(
        (m) => m.VariationEditorComponent,
      ),
  },
  { path: '**', redirectTo: '' },
];
