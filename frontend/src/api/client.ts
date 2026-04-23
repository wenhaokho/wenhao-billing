import axios from "axios";

export const api = axios.create({
  baseURL: "/api/v1",
  withCredentials: true,
});

api.interceptors.response.use(
  (r) => r,
  (err) => {
    if (err?.response?.status === 401 && !window.location.pathname.endsWith("/login")) {
      window.location.href = "/login";
    }
    return Promise.reject(err);
  },
);
