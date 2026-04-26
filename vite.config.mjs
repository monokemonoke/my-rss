import { defineConfig } from 'vite';
import react from '@vitejs/plugin-react';

export default defineConfig({
  plugins: [react()],
  build: {
    assetsDir: '.',
    emptyOutDir: true,
    outDir: 'static/dist',
    rollupOptions: {
      input: 'src/main.jsx',
      output: {
        assetFileNames: 'app.[ext]',
        chunkFileNames: '[name].js',
        entryFileNames: 'app.js',
      },
    },
  },
});
