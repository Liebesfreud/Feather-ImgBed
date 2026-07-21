import { createRouter, createWebHistory } from 'vue-router'
import ApiView from './views/ApiView.vue'
import UploadView from './views/UploadView.vue'

export default createRouter({
  history: createWebHistory(),
  routes: [
    { path: '/', redirect: '/upload' },
    { path: '/upload', name: 'upload', component: UploadView },
    { path: '/gallery', name: 'gallery', component: () => import('./views/GalleryView.vue') },
    { path: '/trash', name: 'trash', component: () => import('./views/TrashView.vue') },
    { path: '/albums', name: 'albums', component: () => import('./views/AlbumsView.vue') },
    { path: '/albums/:id', name: 'album-detail', component: () => import('./views/AlbumDetailView.vue') },
    { path: '/developer', name: 'developer', component: ApiView },
    { path: '/settings', name: 'settings', component: () => import('./views/SettingsView.vue') },
    { path: '/:pathMatch(.*)*', redirect: '/upload' },
  ],
})
