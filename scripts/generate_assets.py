#!/usr/bin/env python3
import os
import base64
from PIL import Image
import cairosvg

def generate():
    os.makedirs('../assets', exist_ok=True)
    svg_path = '../assets/logo.svg'
    png_path = '../assets/logo.png'
    ansi_path = '../assets/logo.ansi'

    print(f"Generating PNG from {svg_path}...")
    cairosvg.svg2png(url=svg_path, write_to=png_path, output_width=640, output_height=320)

    print("Generating ANSI pixel art...")
    # Max 80 columns, Max 25 rows
    target_width = 65  # slightly under 80 for safety
    target_height = 20

    img = Image.open(png_path).convert('RGBA')
    img = img.resize((target_width, target_height * 2), Image.Resampling.LANCZOS)
    
    pixels = img.load()
    ansi_str = ""
    for y in range(target_height):
        for x in range(target_width):
            r1, g1, b1, a1 = pixels[x, y*2]
            r2, g2, b2, a2 = pixels[x, y*2 + 1]
            
            # Using half-blocks. 
            # Transparent should use terminal default (no bg/fg style)
            if a1 < 128 and a2 < 128:
                ansi_str += "\033[0m "
            elif a1 < 128:
                # Top transparent, Bottom solid
                ansi_str += f"\033[0m\033[38;2;{r2};{g2};{b2}m▄"
            elif a2 < 128:
                # Top solid, Bottom transparent
                ansi_str += f"\033[0m\033[38;2;{r1};{g1};{b1}m▀"
            else:
                # Both solid
                ansi_str += f"\033[38;2;{r1};{g1};{b1}m\033[48;2;{r2};{g2};{b2}m▀\033[0m"
        ansi_str += "\033[0m\n"

    with open(ansi_path, 'w') as f:
        f.write(ansi_str)

    print("Successfully generated all assets!")

if __name__ == '__main__':
    # Ensure we run from the scripts directory
    os.chdir(os.path.dirname(os.path.abspath(__file__)))
    generate()
