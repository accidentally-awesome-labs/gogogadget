// Carousel controller, owned by component/carousel.
//
// The scroller and the dot links already work: CSS scroll snap does the
// snapping, and each dot is an anchor the browser scrolls to itself. This file
// only keeps the dots' current state in sync with the scroll position, and
// drives autoplay when a caller explicitly asked for it.
//
// Autoplay is off unless requested, and stays off under reduced motion. Content
// that moves on its own is unreadable for anyone who reads slowly, and it steals
// attention from the rest of the page.
document.addEventListener("alpine:init", () => {
  Alpine.data("uiCarousel", () => ({
    init() {
      const root = this.$root;
      const track = root.querySelector("[data-carousel-track]");
      if (!track) return;
      this.root = root;
      this.track = track;

      this._onScroll = () => this.sync();
      track.addEventListener("scroll", this._onScroll, { passive: true });
      this.sync();

      const wants = root.dataset.carouselAutoplay === "true";
      const reduced = window.matchMedia("(prefers-reduced-motion: reduce)").matches;
      if (!wants || reduced) return;

      this.interval = parseInt(root.dataset.carouselInterval || "", 10) || 4000;

      // Pause on hover and on focus. Autoplay that keeps moving while someone
      // is reading or tabbing through the slides makes both impossible.
      this._pause = () => this.stop();
      this._resume = () => this.start();
      root.addEventListener("pointerenter", this._pause);
      root.addEventListener("pointerleave", this._resume);
      root.addEventListener("focusin", this._pause);
      root.addEventListener("focusout", this._resume);
      // A backgrounded tab must not keep advancing; the user comes back to a
      // slide they never saw arrive.
      this._visibility = () => (document.hidden ? this.stop() : this.start());
      document.addEventListener("visibilitychange", this._visibility);

      this.start();
    },

    slides() {
      return Array.from(this.track.querySelectorAll("[data-carousel-slide]"));
    },

    // sync marks the dot for the slide nearest the scroller's left edge. The
    // filled circle is invisible to assistive technology, so aria-current is
    // what actually reports the position.
    sync() {
      const slides = this.slides();
      if (!slides.length) return;
      const left = this.track.scrollLeft;
      // At the end of the scroll range the last slide can never be nearest the
      // left edge, so "nearest" would report the second-to-last forever. Scrolled
      // to the end means the last slide, which is what the reader sees.
      const atEnd = left + this.track.clientWidth >= this.track.scrollWidth - 2;
      let nearest = slides.length - 1;
      if (!atEnd) {
        let best = Infinity;
        nearest = 0;
        slides.forEach((slide, index) => {
          const distance = Math.abs(slide.offsetLeft - this.track.offsetLeft - left);
          if (distance < best) {
            best = distance;
            nearest = index;
          }
        });
      }
      this.root.querySelectorAll("[data-carousel-dot]").forEach((dot) => {
        const active = parseInt(dot.dataset.carouselDot, 10) === nearest + 1;
        dot.setAttribute("aria-current", active ? "true" : "false");
        dot.classList.toggle("bg-brand", active);
        dot.classList.toggle("bg-surface-raised", !active);
      });
    },

    start() {
      if (this.timer || document.hidden) return;
      this.timer = window.setInterval(() => this.advance(), this.interval);
    },
    stop() {
      if (!this.timer) return;
      window.clearInterval(this.timer);
      this.timer = null;
    },

    // advance scrolls rather than reorders. Moving the elements would break the
    // dot anchors and lose the reader's place in the DOM.
    advance() {
      const slides = this.slides();
      if (slides.length < 2) return;
      const atEnd =
        this.track.scrollLeft + this.track.clientWidth >= this.track.scrollWidth - 2;
      const target = atEnd ? slides[0] : this.nextSlide(slides);
      if (!target) return;
      this.track.scrollTo({
        left: target.offsetLeft - this.track.offsetLeft,
        behavior: "smooth",
      });
    },
    nextSlide(slides) {
      const left = this.track.scrollLeft;
      return slides.find((slide) => slide.offsetLeft - this.track.offsetLeft > left + 1);
    },

    destroy() {
      this.stop();
      this.track.removeEventListener("scroll", this._onScroll);
      if (this._pause) {
        this.root.removeEventListener("pointerenter", this._pause);
        this.root.removeEventListener("pointerleave", this._resume);
        this.root.removeEventListener("focusin", this._pause);
        this.root.removeEventListener("focusout", this._resume);
        document.removeEventListener("visibilitychange", this._visibility);
      }
    },
  }));
});
