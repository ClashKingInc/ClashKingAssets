# ClashKing Assets
---

## Features/Stack

- **FastAPI**: Used to serve the files & assets
  
- **Image Handling (PIL)**: Supports JPEG, PNG, & WebP and can convert between them

- **Cloudflare CDN**: Caches assets for latency & availability

- **Cache-Control**: `Cache-Control` headers, with a default expiration time of 30 days.

---

- **GET `/{file_path:path}`**

  Serve an image file, converting it to the appropriate format if necessary. Applies `Cache-Control` headers to ensure efficient caching.

---

## Contribution

Contributions are welcome! Please fork the repository and submit a pull request. Ensure that you adhere to the coding standards and provide clear documentation for any new features.

---

## License

This project is licensed under the MIT License - see the [LICENSE](LICENSE) file for details.

---

## Contact

For any questions or support, please open an issue in the GitHub repository or contact us at devs@clashkingbot.com.
