import { createRouter, createWebHistory } from 'vue-router'
import UploadView from './views/UploadView.vue'
import GalleryView from './views/GalleryView.vue'
import SettingsView from './views/SettingsView.vue'

export default createRouter({
  history: createWebHistory(),
  routes: [
    { path: '/', redirect: '/upload' },
    { path: '/upload', name: 'upload', component: UploadView },
    { path: '/gallery', name: 'gallery', component: GalleryView },
    { path: '/settings', name: 'settings', component: SettingsView },
    { path: '/:pathMatch(.*)*', redirect: '/upload' },
  ],
})
