import { initializeApp } from 'firebase/app'
import { getAuth, GoogleAuthProvider } from 'firebase/auth'
import type { Auth } from 'firebase/auth'

export const firebaseEnabled = Boolean(import.meta.env.VITE_FIREBASE_API_KEY)

export let auth: Auth | null = null
export const googleProvider = new GoogleAuthProvider()

if (firebaseEnabled) {
  const app = initializeApp({
    apiKey: import.meta.env.VITE_FIREBASE_API_KEY as string,
    authDomain: import.meta.env.VITE_FIREBASE_AUTH_DOMAIN as string,
    projectId: import.meta.env.VITE_FIREBASE_PROJECT_ID as string,
  })
  auth = getAuth(app)
}
