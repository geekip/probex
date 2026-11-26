import { mount } from 'svelte';
import App from '@/app/App.svelte';
import '@/app/styles/app.css';

mount(App, { target: document.getElementById('app')! });
