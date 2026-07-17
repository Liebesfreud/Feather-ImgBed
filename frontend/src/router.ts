import { createRouter, createWebHistory } from 'vue-router'
import UploadView from './views/UploadView.vue'
import GalleryView from './views/GalleryView.vue'
import TrashView from './views/TrashView.vue'
import AlbumsView from './views/AlbumsView.vue'
import AlbumDetailView from './views/AlbumDetailView.vue'
import SettingsView from './views/SettingsView.vue'

export default createRouter({
  history: createWebHistory(),
  routes: [
    { path: '/', redirect: '/upload' },
    { path: '/upload', name: 'upload', component: UploadView },
    { path: '/gallery', name: 'gallery', component: GalleryView },
    { path: '/trash', name: 'trash', component: TrashView },
    { path: '/albums', name: 'albums', component: AlbumsView },
    { path: '/albums/:id', name: 'album-detail', component: AlbumDetailView },
    { path: '/settings', name: 'settings', component: SettingsView },
    { path: '/:pathMatch(.*)*', redirect: '/upload' },
  ],
})
