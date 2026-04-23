import { defineStore } from "pinia";
import { ref, computed } from "vue";
import { api } from "@/api/client";

export interface User {
  user_id: string;
  email: string;
  role: string;
  display_name: string | null;
  created_at: string;
}

export interface ForgotPasswordResponse {
  message: string;
  reset_token: string | null;
  reset_link: string | null;
}

export const useAuthStore = defineStore("auth", () => {
  const user = ref<User | null>(null);

  const isAuthed = computed(() => !!user.value);
  const displayLabel = computed(() => {
    if (!user.value) return "";
    return user.value.display_name?.trim() || user.value.email;
  });

  async function login(email: string, password: string) {
    const { data } = await api.post<User>("/auth/login", { email, password });
    user.value = data;
  }

  async function fetchMe() {
    try {
      const { data } = await api.get<User>("/auth/me");
      user.value = data;
    } catch {
      user.value = null;
    }
  }

  async function logout() {
    await api.post("/auth/logout");
    user.value = null;
  }

  async function updateProfile(payload: { email?: string; display_name?: string | null }) {
    const { data } = await api.patch<User>("/users/me", payload);
    user.value = data;
    return data;
  }

  async function changePassword(current_password: string, new_password: string) {
    const { data } = await api.post<User>("/auth/change-password", {
      current_password,
      new_password,
    });
    user.value = data;
    return data;
  }

  async function forgotPassword(email: string) {
    const { data } = await api.post<ForgotPasswordResponse>("/auth/forgot-password", {
      email,
    });
    return data;
  }

  async function resetPassword(token: string, new_password: string) {
    const { data } = await api.post<User>("/auth/reset-password", {
      token,
      new_password,
    });
    return data;
  }

  return {
    user,
    isAuthed,
    displayLabel,
    login,
    fetchMe,
    logout,
    updateProfile,
    changePassword,
    forgotPassword,
    resetPassword,
  };
});
