import { defineStore } from "pinia";
import { ref, computed } from "vue";
import { api } from "@/api/client";
export const useAuthStore = defineStore("auth", () => {
    const user = ref(null);
    const isAuthed = computed(() => !!user.value);
    const displayLabel = computed(() => {
        if (!user.value)
            return "";
        return user.value.display_name?.trim() || user.value.email;
    });
    async function login(email, password) {
        const { data } = await api.post("/auth/login", { email, password });
        user.value = data;
    }
    async function fetchMe() {
        try {
            const { data } = await api.get("/auth/me");
            user.value = data;
        }
        catch {
            user.value = null;
        }
    }
    async function logout() {
        await api.post("/auth/logout");
        user.value = null;
    }
    async function updateProfile(payload) {
        const { data } = await api.patch("/users/me", payload);
        user.value = data;
        return data;
    }
    async function changePassword(current_password, new_password) {
        const { data } = await api.post("/auth/change-password", {
            current_password,
            new_password,
        });
        user.value = data;
        return data;
    }
    async function forgotPassword(email) {
        const { data } = await api.post("/auth/forgot-password", {
            email,
        });
        return data;
    }
    async function resetPassword(token, new_password) {
        const { data } = await api.post("/auth/reset-password", {
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
